//go:build linux

package detached

import (
	"context"
	"errors"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

// An exact specification can name an older, terminal or queued generation.
// Reviving it beside a newer allocation bypasses replacement and can make the
// same kernel wait for its own promise. Retire that historical authority, then
// let EnsureSession's existing creation path hand off the latest predecessor
// and allocate a monotonically newer kernel generation.
func (b *Backend) handoffHistoricalKernelSession(ctx context.Context, existing workspace.KernelExecutionBackend, spec workspace.KernelExecutionSessionSpecV1) (bool, error) {
	latest, found, err := b.Store.FindLatestKernelExecutionBackendForIdentity(ctx, spec)
	if err != nil || !found || latest.BackendID == existing.BackendID {
		return false, err
	}
	if latest.KernelGeneration <= existing.KernelGeneration {
		return false, workspace.ErrKernelExecutionBackendConflict
	}
	refresh := func(refreshCtx context.Context) (workspace.KernelExecutionBackend, bool, error) {
		current, found, err := b.Store.GetKernelExecutionBackend(refreshCtx, existing.BackendID)
		if err == nil && found && !sameResourceWaitBackend(current, existing) {
			return current, false, workspace.ErrKernelExecutionBackendStale
		}
		return current, found, err
	}
	retireWaiting := func(waitCtx context.Context, expected workspace.KernelExecutionBackend, _ workspace.KernelExecutionSessionSpecV1) (bool, error) {
		current, reservation, eligible, err := b.readUnlaunchedResourceWait(waitCtx, expected)
		if err != nil || !eligible || current.State != workspace.KernelExecutionBackendStateStarting {
			return false, err
		}
		return b.finishUnlaunchedResourceWait(waitCtx, current, reservation)
	}
	retired, dead, err := b.retireKernelSessionPredecessor(ctx, existing, spec, refresh, retireWaiting)
	if err != nil {
		return false, err
	}
	if dead && retired.State != workspace.KernelExecutionBackendStateStopped && retired.State != workspace.KernelExecutionBackendStateEvidenceLost {
		if _, err := b.Store.FinishKernelExecutionBackend(ctx, workspace.FinishKernelExecutionBackendInput{
			BackendID: retired.BackendID, BackendGeneration: retired.BackendGeneration,
			ExecutorInstanceID: retired.ExecutorInstanceID, EvidenceLost: true,
		}); err != nil && !errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
			return false, err
		}
	}
	return true, nil
}

// Keep one handoff implementation for both the latest predecessor and a
// historical exact authority. Only the fenced lookup/never-launched admission
// differs; active-work, control lease, close and physical-release rules do not.
func (b *Backend) retireKernelSessionPredecessor(
	ctx context.Context,
	predecessor workspace.KernelExecutionBackend,
	spec workspace.KernelExecutionSessionSpecV1,
	refresh func(context.Context) (workspace.KernelExecutionBackend, bool, error),
	retireWaiting func(context.Context, workspace.KernelExecutionBackend, workspace.KernelExecutionSessionSpecV1) (bool, error),
) (workspace.KernelExecutionBackend, bool, error) {
	for {
		latest, found, err := refresh(ctx)
		if err != nil {
			return predecessor, false, err
		}
		if !found {
			return predecessor, false, workspace.ErrKernelExecutionBackendStale
		}
		predecessor = latest
		if predecessor.State == workspace.KernelExecutionBackendStateStopped ||
			predecessor.State == workspace.KernelExecutionBackendStateEvidenceLost {
			return waitForPredecessorKernelAuthorityRelease(ctx, predecessor, func(refreshCtx context.Context) (workspace.KernelExecutionBackend, bool, error) {
				return b.Store.GetKernelExecutionBackend(refreshCtx, predecessor.BackendID)
			})
		} else if predecessor.State == workspace.KernelExecutionBackendStateStarting {
			if retired, retireErr := retireWaiting(ctx, predecessor, spec); retireErr != nil {
				return predecessor, false, retireErr
			} else if retired {
				continue
			}
			if ready, waitErr := b.waitForReady(ctx, predecessor.BackendID, predecessor.BackendGeneration, b.StartTimeout); waitErr == nil {
				predecessor = ready
				continue
			} else if err := ctx.Err(); err != nil {
				return predecessor, false, err
			}
			refreshed, found, readErr := b.Store.GetKernelExecutionBackend(ctx, predecessor.BackendID)
			if readErr != nil {
				return predecessor, false, readErr
			}
			if !found || refreshed.BackendGeneration != predecessor.BackendGeneration {
				return predecessor, false, workspace.ErrKernelExecutionBackendStale
			}
			predecessor = refreshed
			if predecessor.State == workspace.KernelExecutionBackendStateStarting {
				if err := b.settleExitedStartup(ctx, predecessor); err != nil {
					return predecessor, false, err
				}
				continue
			}
		} else if predecessor.State == workspace.KernelExecutionBackendStateReady ||
			predecessor.State == workspace.KernelExecutionBackendStateDraining {
			transitioned, idle, err := b.Store.BeginKernelExecutionBackendDrainIfIdle(ctx,
				workspace.BeginKernelExecutionBackendDrainIfIdleInput{
					BackendID: predecessor.BackendID, BackendGeneration: predecessor.BackendGeneration,
					ExecutorInstanceID: predecessor.ExecutorInstanceID,
				})
			if err != nil {
				return predecessor, false, err
			}
			predecessor = transitioned
			if idle {
				ref := backendSessionRef(predecessor)
				lease, err := b.AcquireSessionControl(ctx, ref, 2*time.Minute)
				if err != nil {
					return predecessor, false, err
				}
				if err := b.CloseSession(ctx, ref, lease); err != nil &&
					!errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
					return predecessor, false, err
				}
				continue
			}
		}
		dead, err := predecessorBackendDefinitelyDead(predecessor, time.Now().UTC())
		if err != nil || dead {
			return predecessor, dead, err
		}
		timer := time.NewTimer(defaultBackendPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return predecessor, false, ctx.Err()
		case <-timer.C:
		}
	}
}
