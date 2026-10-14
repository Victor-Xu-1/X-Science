//go:build linux

package detached

import (
	"context"
	"errors"

	workspace "x-science/internal/persistence/workspace"
)

func (b *Backend) reconcileResourceWait(ctx context.Context, backend workspace.KernelExecutionBackend) (bool, error) {
	reservation, found, err := b.Store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil {
		return true, err
	}
	if !found || reservation.State != "waiting" {
		return false, nil
	}
	access, found, err := b.Store.GetKernelFrameAccessContext(ctx, backend.FrameID)
	if err != nil {
		return true, err
	}
	if !found || access.UserID != backend.OwnerUserID || access.Frame.IncarnationID != backend.FrameIncarnationID || access.RootFrameIncarnationID != backend.RootFrameIncarnationID || access.Frame.Status != "processing" {
		cancelled, err := b.Store.CancelWaitingKernelResources(ctx, backend.BackendID, backend.BackendGeneration)
		if err != nil || !cancelled {
			return true, err
		}
		return true, b.Store.FailKernelExecutorStartup(ctx, workspace.KernelStartupFailure{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, Stage: "process_exit"})
	}
	if err := b.launchExecutor(backend); err != nil {
		var waiting *ExecutorResourceUnavailableError
		if errors.As(err, &waiting) {
			return true, nil
		}
		return true, err
	}
	// The existing capacity notice committed with resource admission. Do not
	// introduce a second publication window after external launch/readiness.
	return true, nil
}
