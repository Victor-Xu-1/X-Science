package server

import (
	"context"
	"errors"

	workspace "x-science/internal/persistence/workspace"
)

type kernelNamespaceRecoveryRequiredError struct{ operationID string }

func (e *kernelNamespaceRecoveryRequiredError) Error() string {
	return "The original execution and its results are preserved pending runtime identity verification; no replacement execution was started."
}

func kernelNamespaceRecoveryPending(operationID string) error {
	return &kernelLocalOperationPendingRecoveryError{operationID: operationID, state: workspace.KernelLocalOperationStateStarted,
		cause: &kernelNamespaceRecoveryRequiredError{operationID: operationID}}
}

func kernelNamespaceRecoveryHeld(execution workspace.DetachedKernelExecution) bool {
	return execution.ReasonCode == workspace.KernelExecutionNamespaceRecoveryReasonCode && execution.TerminalReceiptID == ""
}

// The event checkpoint deliberately keeps a foreground tool provisional.
// Re-read its durable marker rather than classify a model/error string as
// permission to stop recovery or manufacture a terminal result.
func (s *Server) kernelNamespaceRecoveryInterruption(ctx context.Context, owner, operationID string) (bool, error) {
	operation, found, err := s.workspaceStore.GetKernelLocalOperation(ctx, owner, operationID)
	if err != nil || !found {
		return false, err
	}
	execution, found, err := s.workspaceStore.GetDetachedKernelExecution(ctx, operation.ExecutionID)
	if err != nil || !found {
		return false, err
	}
	if execution.OperationID != operation.OperationID {
		return false, workspace.ErrDetachedKernelExecutionConflict
	}
	return kernelNamespaceRecoveryHeld(execution), nil
}

func kernelNamespaceRecoveryError(err error) bool {
	var required *kernelNamespaceRecoveryRequiredError
	return errors.As(err, &required)
}
