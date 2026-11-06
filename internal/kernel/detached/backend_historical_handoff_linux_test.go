//go:build linux

package detached

import (
	"context"
	"errors"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolprogress"
)

func TestEnsureSessionHistoricalSpecDoesNotWaitBehindNewerAllocation(t *testing.T) {
	for _, persistedWait := range []bool{false, true} {
		name := "stopped"
		if persistedWait {
			name = "persisted_waiting_after_reopen"
		}
		t.Run(name, func(t *testing.T) {
			f := newHistoricalHandoffFixture(t, persistedWait)
			waiting := make(chan toolprogress.Update, 1)
			ctx, cancel := context.WithTimeout(toolprogress.WithReporter(t.Context(), func(update toolprogress.Update) {
				if update.Phase == "waiting_for_resources" {
					select {
					case waiting <- update:
					default:
					}
				}
			}), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := f.backend.EnsureSession(ctx, f.spec); done <- err }()
			select {
			case update := <-waiting:
				cancel()
				<-done
				t.Fatalf("historical exact spec waits behind its newer same-kernel allocation instead of handing it off: admission=%+v", update.ResourceAdmission)
			case err := <-done:
				if !errors.Is(err, f.launch.err) {
					t.Fatalf("historical exact spec did not reach one authorized replacement launch: %v", err)
				}
			case <-ctx.Done():
				<-done
				t.Fatal("historical handoff did not settle within the bounded fixture")
			}
			if f.closes.Load() != 1 {
				t.Fatalf("latest allocation was not closed once through its control owner: %d", f.closes.Load())
			}
			reservation, found, err := f.backend.Store.KernelResourceReservation(t.Context(), f.latest.BackendID, f.latest.BackendGeneration)
			if err != nil || !found || reservation.State != "released" {
				t.Fatalf("predecessor promise was not released by its owner: %+v found=%t err=%v", reservation, found, err)
			}
			latest, found, err := f.backend.Store.FindLatestKernelExecutionBackendForIdentity(t.Context(), durableSessionSpec(f.spec))
			if err != nil || !found || latest.KernelGeneration <= f.latest.KernelGeneration || latest.BackendID == f.old.BackendID {
				t.Fatalf("historical spec revived an obsolete generation rather than creating the latest authority: %+v found=%t err=%v", latest, found, err)
			}
			select {
			case launched := <-f.launch.launch:
				if launched.BackendID != latest.BackendID || launched.MemoryMaxBytes != f.spec.ResourceMemoryBytes {
					t.Fatalf("replacement lost the immutable explicit budget: %+v", launched)
				}
			default:
				t.Fatal("no replacement launch recorded")
			}
			if persistedWait {
				old, found, err := f.backend.Store.GetKernelExecutionBackend(t.Context(), f.old.BackendID)
				if err != nil || !found || old.State != workspace.KernelExecutionBackendStateStopped {
					t.Fatalf("persisted zero-allocation wait was not settled: %+v found=%t err=%v", old, found, err)
				}
			}
		})
	}
}
