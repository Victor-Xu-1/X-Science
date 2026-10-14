//go:build linux

package detached

import (
	"context"

	workspace "x-science/internal/persistence/workspace"
)

// A changed session specification must not wait for an obsolete allocation to
// launch. Only a never-admitted generation with positive supervisor absence can
// be retired here; an admitted or physically ambiguous predecessor follows the
// existing readiness/drain protocol instead.
func (b *Backend) retireUnlaunchedResourceWait(ctx context.Context, expected workspace.KernelExecutionBackend, desired workspace.KernelExecutionSessionSpecV1) (bool, error) {
	current, reservation, eligible, err := b.readUnlaunchedResourceWait(ctx, expected)
	if err != nil || !eligible {
		return false, err
	}
	// A matching generation may have appeared since EnsureSession's first
	// lookup. Reuse the Store's canonical spec authority, not a second encoder.
	exact, found, err := b.Store.FindKernelExecutionBackendForSession(ctx, desired)
	if err != nil || (found && exact.SessionSpecSHA256 == current.SessionSpecSHA256 && exact.SessionSpecJSON == current.SessionSpecJSON) {
		return false, err
	}
	state, err := b.Launcher.Observe(ctx, expected.BackendID, expected.BackendGeneration)
	if err != nil || state != ExecutorLaunchExited {
		return false, err
	}
	if reservation.State == "waiting" {
		cancelled, err := b.Store.CancelWaitingKernelResources(ctx, expected.BackendID, expected.BackendGeneration)
		if err != nil || !cancelled {
			// The existing supervisor may have won admission after observation.
			// Losing this CAS never grants permission to finish its backend.
			return false, err
		}
	}
	current, reservation, eligible, err = b.readUnlaunchedResourceWait(ctx, expected)
	if err != nil || !eligible {
		return false, err
	}
	if reservation.State != "released" {
		return false, workspace.ErrKernelExecutionBackendStale
	}
	// Every physical launch first calls AdmitKernelResources, which rejects a
	// released row. Thus no legal startup can race this finish after our winning
	// cancellation fence. Zero reserved bytes also makes an interrupted fence
	// recoverable without mistaking a previously launched generation for one.
	finished, err := b.Store.FinishKernelExecutionBackend(ctx, workspace.FinishKernelExecutionBackendInput{
		BackendID: current.BackendID, BackendGeneration: current.BackendGeneration,
		ExecutorInstanceID: current.ExecutorInstanceID,
	})
	if err != nil {
		return false, err
	}
	if !sameResourceWaitBackend(finished, expected) || finished.State != workspace.KernelExecutionBackendStateStopped {
		return false, workspace.ErrKernelExecutionBackendStale
	}
	return true, nil
}

func (b *Backend) readUnlaunchedResourceWait(ctx context.Context, expected workspace.KernelExecutionBackend) (workspace.KernelExecutionBackend, workspace.KernelResourceReservation, bool, error) {
	var reservation workspace.KernelResourceReservation
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, expected.BackendID)
	if err != nil {
		return current, reservation, false, err
	}
	if !found || !sameResourceWaitBackend(current, expected) {
		return current, reservation, false, workspace.ErrKernelExecutionBackendStale
	}
	if !unlaunchedResourceWaitBackend(current) {
		return current, reservation, false, nil
	}
	reservation, found, err = b.Store.KernelResourceReservation(ctx, expected.BackendID, expected.BackendGeneration)
	if err != nil || !found {
		return current, reservation, false, err
	}
	if reservation.BootID != current.MachineBootID || reservation.BackendID != current.BackendID || reservation.Generation != current.BackendGeneration {
		return current, reservation, false, workspace.ErrKernelExecutionBackendStale
	}
	if reservation.ReservedBytes != 0 || (reservation.State != "waiting" && reservation.State != "released") {
		return current, reservation, false, nil
	}
	spec, err := workspace.DecodeKernelExecutionSessionSpecV1(current.SessionSpecJSON)
	if err != nil {
		return current, reservation, false, err
	}
	if reservation.RequestedBytes != spec.ResourceMemoryBytes {
		return current, reservation, false, workspace.ErrKernelExecutionBackendConflict
	}
	return current, reservation, true, nil
}

func unlaunchedResourceWaitBackend(current workspace.KernelExecutionBackend) bool {
	return current.State == workspace.KernelExecutionBackendStateStarting &&
		current.ExecutorPID == 0 && current.ExecutorPIDStartTicks == 0 &&
		current.WorkerPID == 0 && current.WorkerPIDStartTicks == 0 && current.WorkerPGID == 0 &&
		current.CgroupPath == "" && current.HeartbeatSequence == 0 && current.HeartbeatAt == nil
}
