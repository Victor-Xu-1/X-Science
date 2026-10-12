package server

import (
	"context"
	"errors"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestNamespaceKernelHeldObservationPreservesOperationAndBoundsRecovery(t *testing.T) {
	f := newDetachedRecoveryFixture(t, true)
	ctx := context.Background()
	execution, err := f.server.workspaceStore.MarkKernelExecutionNamespaceRecovery(ctx, workspace.MarkKernelExecutionNamespaceRecoveryInput{
		OwnerUserID: f.operation.OwnerUserID, OperationID: f.operation.OperationID, ExecutionID: f.execution.ExecutionID,
		BackendGeneration: f.execution.BackendGeneration, ExecutorInstanceID: f.backend.ExecutorInstanceID, ExpectedVersion: f.execution.StateVersion,
	})
	if err != nil || !kernelNamespaceRecoveryHeld(execution) || execution.State != workspace.DetachedKernelExecutionStateStarted || execution.TerminalReceiptID != "" {
		t.Fatal("recovery observation fabricated terminal evidence", execution, err)
	}
	f.execution = execution
	if err := f.server.recoverDetachedKernelExecution(ctx, workspace.DetachedKernelExecutionRecoveryCandidate{Operation: f.operation, Execution: f.execution}); err != nil {
		t.Fatal("held recovery tried a provider observation", err)
	}
	ref := kernelruntime.BackendExecutionRef{ExecutionID: execution.ExecutionID, OperationID: execution.OperationID,
		BackendID: execution.BackendID, BackendGeneration: execution.BackendGeneration, RequestSHA256: execution.RequestSHA256, ConfinementSHA256: execution.ConfinementSHA256}
	window, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = f.server.waitDetachedAgentKernelExecution(window, f.access, f.spec, kernelruntime.BackendSessionRef{}, kernelruntime.ExecutionStarted{}, ref, kernelruntime.BackendControlLease{}, 1024, &f.operation, &f.claim)
	var pending *kernelLocalOperationPendingRecoveryError
	if !errors.As(err, &pending) || !kernelNamespaceRecoveryError(err) {
		t.Fatal("held observation did not preserve provisional tool protocol", err)
	}
	held, err := f.server.kernelNamespaceRecoveryInterruption(ctx, f.operation.OwnerUserID, f.operation.OperationID)
	if err != nil || !held || !runnerInterruptionMayContinueSameTask(sessionRunnerExecutionIdentityRecoveryReasonCode) ||
		runnerInterruptionAutoResume(sessionRunnerExecutionIdentityRecoveryReasonCode) || frameResumeRunnerShouldAutoResume(true, SessionRunnerCycleResult{}, sessionRunnerExecutionIdentityRecoveryReasonCode) {
		t.Fatal("recovery interruption would fail or repeat the unchanged task", held, err)
	}
	op, found, err := f.server.workspaceStore.GetKernelLocalOperation(ctx, f.operation.OwnerUserID, f.operation.OperationID)
	if err != nil || !found || op.State != workspace.KernelLocalOperationStateStarted || len(op.ResultJSON) != 0 || op.TerminalAt != nil {
		t.Fatal("provisional operation was falsely settled", op, err)
	}
	notifications, err := f.server.workspaceStore.ConsumeUnreadNotifications(ctx, f.access.Frame.ID, f.access.Frame.RootFrameID, f.access.UserID, 10)
	if err != nil || len(notifications) != 1 || notifications[0].NotificationType != "execution_recovery_required" {
		t.Fatal("missing or duplicated visible recovery observation", notifications, err)
	}
	projected, err := f.server.projectAgentKernelNotifications(ctx, f.access, notifications)
	if err != nil || projected["cells_completed"] != nil {
		t.Fatal("recovery notification claimed physical completion", projected, err)
	}
}
