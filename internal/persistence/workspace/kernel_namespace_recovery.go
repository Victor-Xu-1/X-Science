package workspace

import (
	"context"
	"errors"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

const KernelExecutionNamespaceRecoveryReasonCode = "namespace_recovery_unconfirmed"

type MarkKernelExecutionNamespaceRecoveryInput struct {
	OwnerUserID, OperationID, ExecutionID, ExecutorInstanceID string
	BackendGeneration, ExpectedVersion                        int64
}

// An identity-observation gap is not evidence of physical failure. Preserve
// the started operation and execution; publish one owner-scoped recovery fact
// rather than manufacturing a terminal receipt or dispatching another job.
func (s *Store) MarkKernelExecutionNamespaceRecovery(ctx context.Context, input MarkKernelExecutionNamespaceRecoveryInput) (DetachedKernelExecution, error) {
	if s == nil || s.db == nil || ctx == nil || !validDetachedIdentity(input.OwnerUserID) || !validDetachedIdentity(input.OperationID) ||
		!validDetachedIdentity(input.ExecutionID) || !validDetachedIdentity(input.ExecutorInstanceID) || input.BackendGeneration <= 0 || input.ExpectedVersion <= 0 {
		return DetachedKernelExecution{}, errors.New("complete namespace recovery observation authority is required")
	}
	return s.transitionDetachedKernelExecutionWithExecutor(ctx, input.ExecutionID, input.BackendGeneration, input.ExecutorInstanceID,
		func(tx *transcriptstore.ImmediateTransaction, current DetachedKernelExecution, now time.Time) (DetachedKernelExecution, error) {
			if current.OperationID != input.OperationID || current.State != DetachedKernelExecutionStateStarted || current.TerminalReceiptID != "" {
				return DetachedKernelExecution{}, ErrDetachedKernelExecutionConflict
			}
			operation, found, err := getKernelLocalOperationQuery(ctx, tx, current.OperationID)
			if err != nil {
				return DetachedKernelExecution{}, err
			}
			if !found || operation.OwnerUserID != input.OwnerUserID || operation.State != KernelLocalOperationStateStarted || operation.ExecutionID != current.ExecutionID {
				return DetachedKernelExecution{}, ErrDetachedKernelExecutionConflict
			}
			if err := validateKernelLocalOperationCurrentAuthority(ctx, tx, operation, false); err != nil {
				return DetachedKernelExecution{}, err
			}
			backend, found, err := getKernelExecutionBackendQuery(ctx, tx, current.BackendID)
			if err != nil {
				return DetachedKernelExecution{}, err
			}
			if !found || backend.OwnerUserID != operation.OwnerUserID || backend.ProjectID != operation.ProjectID || backend.RootFrameID != operation.RootFrameID || backend.FrameID != operation.FrameID || backend.KernelID != operation.KernelID || backend.KernelGeneration != operation.KernelGeneration {
				return DetachedKernelExecution{}, ErrKernelExecutionBackendStale
			}
			if backend.RootFrameIncarnationID != operation.RootFrameIncarnationID || backend.FrameIncarnationID != operation.FrameIncarnationID || current.ConfinementSHA256 != operation.ConfinementSHA256 {
				return DetachedKernelExecution{}, ErrKernelExecutionBackendStale
			}
			if current.ReasonCode == KernelExecutionNamespaceRecoveryReasonCode {
				if input.ExpectedVersion != current.StateVersion && input.ExpectedVersion != current.StateVersion-1 {
					return DetachedKernelExecution{}, ErrDetachedKernelExecutionConflict
				}
				return current, nil
			}
			if current.StateVersion != input.ExpectedVersion {
				return DetachedKernelExecution{}, ErrDetachedKernelExecutionConflict
			}
			updated, err := updateDetachedKernelExecutionTx(ctx, tx, current, `reason_code=?,state_version=state_version+1,updated_at=?`, KernelExecutionNamespaceRecoveryReasonCode, now.Format(time.RFC3339Nano))
			if err != nil {
				return DetachedKernelExecution{}, err
			}
			_, _, err = createNotificationTx(ctx, tx, CreateNotificationInput{ID: "x-science.kernel.namespace-recovery:" + current.ExecutionID, SenderFrameID: operation.FrameID, RecipientFrameID: operation.FrameID, RootFrameID: operation.RootFrameID, OwnerUserID: operation.OwnerUserID, NotificationType: "execution_recovery_required", Payload: map[string]any{"execution_id": current.ExecutionID, "operation_id": current.OperationID, "reason_code": KernelExecutionNamespaceRecoveryReasonCode, "recovery_required": true, "physical_outcome": "unconfirmed", "logical_task_complete": false, "replacement_execution_started": false}}, now)
			return updated, err
		})
}
