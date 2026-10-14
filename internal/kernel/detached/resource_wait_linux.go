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
	if found && reservation.State == "released" {
		return b.reconcileReleasedResourceWait(ctx, backend)
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
		return b.reconcileReleasedResourceWait(ctx, backend)
	}
	if err := b.launchExecutor(backend); err != nil {
		var waiting *ExecutorResourceUnavailableError
		if errors.As(err, &waiting) {
			return true, nil
		}
		if errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
			if handled, settleErr := b.reconcileReleasedResourceWait(ctx, backend); handled {
				return true, settleErr
			}
		}
		return true, err
	}
	// The existing capacity notice committed with resource admission. Do not
	// introduce a second publication window after external launch/readiness.
	return true, nil
}

func (b *Backend) reconcileReleasedResourceWait(ctx context.Context, backend workspace.KernelExecutionBackend) (bool, error) {
	handled, err := b.settleReleasedResourceWait(ctx, backend)
	if errors.Is(err, errStartupStillOwned) || errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
		return true, nil // Existing supervisor will revisit pending/advanced ownership.
	}
	return handled || err != nil, err
}
