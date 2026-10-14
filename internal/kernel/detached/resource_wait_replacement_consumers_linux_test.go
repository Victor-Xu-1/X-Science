//go:build linux

package detached

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestResourceWaitReplacementSupervisorResumesReleaseWithoutFailure(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if cancelled, err := b.Store.CancelWaitingKernelResources(ctx, previous.BackendID, previous.BackendGeneration); err != nil || !cancelled {
		t.Fatalf("could not establish the released interruption: cancelled=%t err=%v", cancelled, err)
	}
	if err := b.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.Open(filepath.Join(spec.WorkspaceDir, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	b.Store = reopened
	// Exercise the existing supervisor's expired-startup consumer, not only a
	// subsequent EnsureSession call. Keep the production-valid five-second
	// startup window instead of forging a historical timestamp or invalid TTL.
	timer := time.NewTimer(max(time.Until(previous.UpdatedAt.Add(b.StartTimeout)), 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	if err := b.ReconcileStartups(ctx); err != nil {
		t.Fatal(err)
	}
	assertReleasedResourceWaitStoppedWithoutFailure(t, b, previous)
	if len(launcher.launches) != 0 {
		t.Fatalf("supervisor launched a released generation: %+v", launcher.launches)
	}
	spec.ResourceMemoryBytes = 1 << 30
	if _, err := b.EnsureSession(ctx, spec); !errors.Is(err, launcher.launchError) || len(launcher.launches) != 1 {
		t.Fatalf("replacement did not follow normal supervisor retirement once: launches=%+v err=%v", launcher.launches, err)
	}
}

func TestResourceWaitReplacementSupervisorAdmissionRaceWithoutFailure(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	launcher.capacityHook = func(ctx context.Context) error {
		// ReconcileStartups has already read waiting, but its existing resource
		// planner has not yet admitted the original generation.
		cancelled, err := b.Store.CancelWaitingKernelResources(ctx, previous.BackendID, previous.BackendGeneration)
		if err != nil || !cancelled {
			t.Fatalf("replacement did not win the admission race: cancelled=%t err=%v", cancelled, err)
		}
		return nil
	}
	reconcileErr := b.ReconcileStartups(ctx)
	assertReleasedResourceWaitStoppedWithoutFailure(t, b, previous)
	if reconcileErr != nil || len(launcher.launches) != 0 {
		t.Fatalf("superseded admission became a supervisor failure or launched: launches=%+v err=%v", launcher.launches, reconcileErr)
	}
	spec.ResourceMemoryBytes = 1 << 30
	if _, err := b.EnsureSession(ctx, spec); !errors.Is(err, launcher.launchError) || len(launcher.launches) != 1 {
		t.Fatalf("replacement did not launch exactly once after the fenced race: launches=%+v err=%v", launcher.launches, err)
	}
}

func TestResourceWaitReplacementSupervisorPreservesOwnershipOnCancellation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		observation ExecutorLaunchState
		cancelOwner bool
	}{
		{"inactive_owner_exited", ExecutorLaunchExited, true},
		{"released_alive", ExecutorLaunchAlive, false},
		{"released_unknown", ExecutorLaunchUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, previous, launcher := resourceReplacementFixture(t)
			ctx := context.Background()
			launcher.observation = tc.observation
			if tc.cancelOwner {
				repo, err := b.Store.TranscriptRepository(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
					UID: "resource-owner-stream", OwnerID: previous.OwnerUserID, ExternalID: previous.FrameID, SessionID: previous.FrameID,
					Kind: transcriptstore.StreamKindFrameRef, ProjectID: previous.ProjectID, RootFrameID: previous.RootFrameID,
					FrameID: previous.FrameID, Epoch: 1,
				}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := b.Store.CancelFrameWithTranscript(ctx, previous.FrameID); err != nil {
					t.Fatal(err)
				}
			} else if cancelled, err := b.Store.CancelWaitingKernelResources(ctx, previous.BackendID, previous.BackendGeneration); err != nil || !cancelled {
				t.Fatalf("release failed: cancelled=%t err=%v", cancelled, err)
			}
			if err := b.ReconcileStartups(ctx); err != nil {
				t.Fatal(err)
			}
			if len(launcher.launches) != 0 {
				t.Fatalf("cancelled resource wait launched: %+v", launcher.launches)
			}
			if tc.cancelOwner {
				assertReleasedResourceWaitStoppedWithoutFailure(t, b, previous)
				return
			}
			current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
			if err != nil || !found || current.State != previous.State || current.StateVersion != previous.StateVersion {
				t.Fatalf("unproven released generation was retired: %+v found=%t err=%v", current, found, err)
			}
			if receipt, found, err := b.Store.GetKernelStartupFailure(ctx, previous.BackendID, previous.BackendGeneration); err != nil || found {
				t.Fatalf("unproven released generation gained failure evidence: %+v found=%t err=%v", receipt, found, err)
			}
		})
	}
}

func assertReleasedResourceWaitStoppedWithoutFailure(t *testing.T, b *Backend, previous workspace.KernelExecutionBackend) {
	t.Helper()
	ctx := context.Background()
	current, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStopped || current.SessionSpecSHA256 != previous.SessionSpecSHA256 || current.ExecutorPID != 0 || current.WorkerPID != 0 {
		t.Fatalf("released generation did not stop without changing physical/input authority: %+v found=%t err=%v", current, found, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(ctx, previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "released" || reservation.ReservedBytes != 0 || reservation.RequestedBytes != 0 {
		t.Fatalf("released ledger proof changed: %+v found=%t err=%v", reservation, found, err)
	}
	if receipt, found, err := b.Store.GetKernelStartupFailure(ctx, previous.BackendID, previous.BackendGeneration); err != nil || found {
		t.Fatalf("never-launched generation gained a fabricated failure receipt: %+v found=%t err=%v", receipt, found, err)
	}
}
