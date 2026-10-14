//go:build linux

package detached

import (
	"context"
	"errors"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolprogress"
)

// A capacity wait belongs to the original EnsureSession invocation. Returning
// it as a completed not-started tool would cancel that invocation's approved
// operation and require a new model proposal. Only the existing startup recovery
// supervisor may admit and launch a queued generation; this waiter observes it.
func (b *Backend) launchExecutorWithResourceWait(ctx context.Context, backend workspace.KernelExecutionBackend) error {
	if err := ctx.Err(); err != nil {
		return resourceWaitContextError(ctx)
	}
	err := b.launchExecutor(backend)
	var unavailable *ExecutorResourceUnavailableError
	if !errors.As(err, &unavailable) {
		return err
	}
	if unavailable.BackendID != backend.BackendID || unavailable.Generation != backend.BackendGeneration {
		return workspace.ErrKernelExecutionBackendConflict
	}
	return b.waitForSessionResourceCapacity(ctx, backend)
}

func (b *Backend) waitForSessionResourceCapacity(ctx context.Context, expected workspace.KernelExecutionBackend) error {
	ctx, cancelWait := resourceAdmissionContext(ctx)
	defer cancelWait()
	// Outbox delivery is a wake hint, not admission authority. Keep a low-rate
	// durable readback for restarts or commits made by a different Store handle.
	ticker := time.NewTicker(defaultExecutorHeartbeatInterval)
	defer ticker.Stop()
	reported := false
	for {
		if err := ctx.Err(); err != nil {
			return resourceWaitContextError(ctx)
		}
		wake, retentionWake := b.Store.OutboxWake(), b.Store.KernelRetentionWake()
		current, found, err := b.Store.GetKernelExecutionBackend(ctx, expected.BackendID)
		if err != nil {
			return err
		}
		if !found || !sameResourceWaitBackend(current, expected) {
			return workspace.ErrKernelExecutionBackendStale
		}
		access, found, err := b.Store.GetKernelFrameAccessContext(ctx, expected.FrameID)
		if err != nil {
			return err
		}
		if !found || access.UserID != expected.OwnerUserID || access.Frame.ProjectID != expected.ProjectID ||
			access.Frame.IncarnationID != expected.FrameIncarnationID || access.Frame.RootFrameID != expected.RootFrameID ||
			access.RootFrameIncarnationID != expected.RootFrameIncarnationID || access.Frame.Status != "processing" {
			return workspace.ErrKernelExecutionBackendStale
		}
		reservation, found, err := b.Store.KernelResourceReservation(ctx, expected.BackendID, expected.BackendGeneration)
		if err != nil {
			return err
		}
		if !found || reservation.BootID != expected.MachineBootID ||
			(current.State != workspace.KernelExecutionBackendStateStarting && current.State != workspace.KernelExecutionBackendStateReady) {
			return workspace.ErrKernelExecutionBackendStale
		}
		switch reservation.State {
		case "reserved":
			// Admission is not readiness. EnsureSession still uses its original
			// bounded readiness check after this resource-only wait returns.
			if err := ctx.Err(); err != nil {
				return resourceWaitContextError(ctx)
			}
			toolprogress.Report(ctx, toolprogress.Update{Phase: "starting", Message: "Execution capacity admitted; waiting for the original runtime to become ready.", Indeterminate: true})
			return nil
		case "waiting":
			if current.State != workspace.KernelExecutionBackendStateStarting {
				return workspace.ErrKernelExecutionBackendConflict
			}
		default:
			return workspace.ErrKernelExecutionBackendStale
		}
		if !reported {
			toolprogress.Report(ctx, toolprogress.Update{Phase: "waiting_for_resources", Message: "Waiting for execution capacity; the original operation is preserved and no computation has started.", Indeterminate: true})
			reported = true
		}
		select {
		case <-ctx.Done():
			return resourceWaitContextError(ctx)
		case <-wake:
		case <-retentionWake:
		case <-ticker.C:
		}
	}
}

// Merge once per queue wait, including its SQLite reads, not once per poll.
// Cancelling this derived context never cancels admitted startup's parent.
func resourceAdmissionContext(setup context.Context) (context.Context, context.CancelFunc) {
	queued, cancel := context.WithCancelCause(setup)
	stop := func() bool { return true }
	if source, ok := setup.(kernelruntime.ResourceAdmissionWaitContextSource); ok {
		if original := source.ResourceAdmissionWaitContext(); original != nil {
			stop = context.AfterFunc(original, func() { cancel(resourceWaitContextError(original)) })
			if original.Err() != nil {
				cancel(resourceWaitContextError(original))
			}
		}
	}
	if setup.Err() != nil {
		cancel(resourceWaitContextError(setup))
	}
	return queued, func() { stop(); cancel(nil) }
}

func resourceWaitContextError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

func sameResourceWaitBackend(current, expected workspace.KernelExecutionBackend) bool {
	return current.BackendID == expected.BackendID && current.BackendGeneration == expected.BackendGeneration &&
		current.MachineBootID == expected.MachineBootID && current.ExecutorInstanceID == expected.ExecutorInstanceID &&
		current.OwnerUserID == expected.OwnerUserID && current.ProjectID == expected.ProjectID &&
		current.FrameID == expected.FrameID && current.FrameIncarnationID == expected.FrameIncarnationID &&
		current.RootFrameID == expected.RootFrameID && current.RootFrameIncarnationID == expected.RootFrameIncarnationID &&
		current.KernelID == expected.KernelID && current.KernelGeneration == expected.KernelGeneration &&
		current.SessionSpecSHA256 == expected.SessionSpecSHA256 && current.ProtocolVersion == expected.ProtocolVersion &&
		current.SocketPath == expected.SocketPath
}
