//go:build linux

package detached

import (
	"context"
	"errors"
	"strconv"

	workspace "synon-go/internal/persistence/workspace"
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
	// Infrastructure availability is not an execution result. The existing
	// notification/recovery path can now retry the original authorized call.
	_, _, err = b.Store.CreateNotification(ctx, workspace.CreateNotificationInput{ID: "kernel-resources-" + backend.BackendID + "-" + strconv.FormatInt(backend.BackendGeneration, 10), SenderFrameID: backend.FrameID, RecipientFrameID: backend.FrameID, RootFrameID: backend.RootFrameID, OwnerUserID: backend.OwnerUserID, NotificationType: "resource_capacity_admitted", Payload: map[string]any{"backend_id": backend.BackendID, "backend_generation": backend.BackendGeneration, "kernel_id": backend.KernelID, "executed": false, "status": "execution_capacity_admitted", "memory_budget_bytes": reservation.RequestedBytes}})
	return true, err
}
