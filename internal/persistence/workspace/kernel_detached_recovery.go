package workspace

import (
	"context"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// DetachedKernelEvidenceLossFence binds an already fenced execution to its
// original executor incarnation. It cannot authorize a new physical execution.
type DetachedKernelEvidenceLossFence struct {
	BackendID          string
	BackendGeneration  int64
	ExecutorInstanceID string
	RequestSHA256      string
	ConfinementSHA256  string
}

// MarkDetachedKernelLocalOperationOutcomeUnknown materializes executor loss
// even during the original server boot. Generic restart recovery retains its
// stricter old-boot/no-live-runner contract.
func (s *Store) MarkDetachedKernelLocalOperationOutcomeUnknown(
	ctx context.Context,
	input MarkKernelLocalOperationOutcomeUnknownInput,
	fence DetachedKernelEvidenceLossFence,
) (KernelLocalOperation, error) {
	if strings.TrimSpace(fence.BackendID) == "" || fence.BackendGeneration <= 0 ||
		strings.TrimSpace(fence.ExecutorInstanceID) == "" || !validLowerHexSHA256(fence.RequestSHA256) ||
		!validLowerHexSHA256(fence.ConfinementSHA256) {
		return KernelLocalOperation{}, ErrDetachedKernelExecutionConflict
	}
	return s.markKernelLocalOperationOutcomeUnknown(ctx, input, &fence)
}

func validateDetachedKernelEvidenceLossTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	op KernelLocalOperation, executionID string, fence DetachedKernelEvidenceLossFence,
) error {
	execution, found, err := getDetachedKernelExecutionQuery(ctx, tx, executionID)
	if err != nil {
		return err
	}
	if !found || op.ExecutionID != executionID || execution.OperationID != op.OperationID ||
		execution.BackendID != fence.BackendID || execution.BackendGeneration != fence.BackendGeneration ||
		execution.RequestSHA256 != fence.RequestSHA256 || execution.ConfinementSHA256 != fence.ConfinementSHA256 ||
		op.ConfinementSHA256 != fence.ConfinementSHA256 || execution.TerminalReceiptID != "" ||
		execution.State != DetachedKernelExecutionStateEvidenceLost {
		return ErrDetachedKernelExecutionConflict
	}
	backend, found, err := getKernelExecutionBackendQuery(ctx, tx, fence.BackendID)
	if err != nil {
		return err
	}
	if !found || backend.BackendGeneration != fence.BackendGeneration || backend.ExecutorInstanceID != fence.ExecutorInstanceID ||
		backend.OwnerUserID != op.OwnerUserID || backend.ProjectID != op.ProjectID ||
		backend.RootFrameID != op.RootFrameID || backend.RootFrameIncarnationID != op.RootFrameIncarnationID ||
		backend.FrameID != op.FrameID || backend.FrameIncarnationID != op.FrameIncarnationID ||
		backend.KernelID != op.KernelID || backend.KernelGeneration != op.KernelGeneration ||
		(backend.State != KernelExecutionBackendStateStopped && backend.State != KernelExecutionBackendStateEvidenceLost) {
		return ErrKernelExecutionBackendStale
	}
	return validateKernelLocalOperationCurrentAuthority(ctx, tx, op, false)
}
