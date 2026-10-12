//go:build linux

package detached

import (
	"context"
	"errors"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/software/localcontainer"
)

func (e *Executor) containerRuntimeIdentity(ctx context.Context, request workspace.KernelDetachedExecutionRequestV1, environment localcontainer.Environment) (*localcontainer.RuntimeIdentity, bool, error) {
	operation, found, err := e.Store.GetKernelLocalOperation(ctx, request.OwnerUserID, request.OperationID)
	if err != nil || !found || operation.ExecutionID != request.ExecutionID || operation.Environment != request.Environment || operation.InputSHA256 == "" {
		return nil, false, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, errors.New("container operation authority cannot be verified"), err)
	}
	cutover, cutoffFound, err := e.Store.GetKernelNamespaceCutoverReceipt(ctx, request.OwnerUserID, operation.OperationID)
	if err != nil {
		return nil, false, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, err)
	}
	previousStarted := cutoffFound && cutover.OriginalState == workspace.KernelLocalOperationStateStarted
	if previousStarted && (cutover.ExecutionID != request.ExecutionID || cutover.InputSHA256 != operation.InputSHA256 || cutover.Environment != operation.Environment ||
		cutover.KernelID != request.KernelID || cutover.KernelGeneration != request.KernelGeneration) {
		return nil, true, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, errors.New("historical container cutoff receipt differs from the immutable execution authority"))
	}
	receipt, found, err := e.Store.GetKernelRuntimeIdentity(ctx, request.OwnerUserID, operation.OperationID)
	if err != nil {
		return nil, previousStarted, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, err)
	}
	if !found {
		if previousStarted {
			return nil, true, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, errors.New("historical container has no actual runtime identity receipt; execution is preserved for verification"))
		}
		return nil, false, nil
	}
	previous := receipt.NamespaceOrigin == "pre-cutover"
	if previous && !previousStarted {
		return nil, true, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, errors.New("historical container identity has no matching cutoff receipt"))
	}
	if receipt.RuntimeKind != localcontainer.ProviderID || receipt.ExecutionID != request.ExecutionID || receipt.InputSHA256 != operation.InputSHA256 ||
		receipt.Environment != request.Environment || receipt.EnvironmentGeneration != environment.HostGeneration || receipt.ImageID != environment.ImageID {
		return nil, previous, errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, errors.New("container runtime receipt conflicts with the immutable request"))
	}
	return &localcontainer.RuntimeIdentity{ExecutionID: receipt.ExecutionID, ContainerID: receipt.RuntimeID, ImageID: receipt.ImageID, Environment: receipt.Environment, EnvironmentGeneration: receipt.EnvironmentGeneration}, previous, nil
}

func (e *Executor) recordContainerRuntimeIdentity(request workspace.KernelDetachedExecutionRequestV1, identity localcontainer.RuntimeIdentity) error {
	ctx := context.Background()
	operation, found, err := e.Store.GetKernelLocalOperation(ctx, request.OwnerUserID, request.OperationID)
	if err != nil || !found || operation.ExecutionID != request.ExecutionID || operation.Environment != identity.Environment {
		return errors.Join(errors.New("container operation authority changed before runtime receipt publication"), err)
	}
	_, err = e.Store.RecordKernelRuntimeIdentity(ctx, request.OwnerUserID, workspace.KernelRuntimeIdentityReceipt{
		OperationID: operation.OperationID, ExecutionID: identity.ExecutionID, InputSHA256: operation.InputSHA256,
		RuntimeKind: localcontainer.ProviderID, RuntimeID: identity.ContainerID, Environment: identity.Environment,
		EnvironmentGeneration: identity.EnvironmentGeneration, ImageID: identity.ImageID,
	})
	return err
}

func (e *Executor) recordContainerRecoveryRequired(request workspace.KernelDetachedExecutionRequestV1, cause error) error {
	if !errors.Is(cause, localcontainer.ErrExecutionOutcomeUnconfirmed) {
		return cause
	}
	ctx := context.Background()
	execution, found, err := e.Store.GetDetachedKernelExecution(ctx, request.ExecutionID)
	if err != nil || !found || execution.OperationID != request.OperationID || execution.BackendID != e.BackendID || execution.BackendGeneration != e.BackendGeneration {
		return errors.Join(localcontainer.ErrExecutionOutcomeUnconfirmed, err)
	}
	if execution.TerminalReceiptID != "" {
		return nil // A real terminal receipt won the observation race.
	}
	_, err = e.Store.MarkKernelExecutionNamespaceRecovery(ctx, workspace.MarkKernelExecutionNamespaceRecoveryInput{
		OwnerUserID: request.OwnerUserID, OperationID: request.OperationID, ExecutionID: request.ExecutionID,
		BackendGeneration: e.BackendGeneration, ExecutorInstanceID: e.ExecutorInstanceID, ExpectedVersion: execution.StateVersion,
	})
	return err
}
