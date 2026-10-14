//go:build linux

package detached

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

// Use the production meminfo planner with controlled physical capacity, while
// forbidding any actual launch or process discovery in these SQLite tests.
type feasibilityTestLauncher struct {
	SystemdUserExecutorLauncher
	t        *testing.T
	state    ExecutorLaunchState
	observed int
}

func (l *feasibilityTestLauncher) Launch(ExecutorLaunchRequest) error {
	l.t.Error("feasibility admission must not launch computation")
	return errors.New("unexpected launch")
}

func (l *feasibilityTestLauncher) Observe(context.Context, string, int64) (ExecutorLaunchState, error) {
	l.observed++
	return l.state, nil
}

func feasibilityMeminfo(t *testing.T, total, available int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", total/1024, available/1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExplicitKernelBudgetExceedsCurrentHostCapacityWithoutReservation(t *testing.T) {
	for _, test := range []struct {
		name             string
		total, requested int64
	}{
		{"explicit_above_host_bound", 12 << 30, 10 << 30},
		{"host_below_reserve", 2 << 30, executorMemoryFloorBytes},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, base := startupBackendFixture(t)
			backend := resourceObservationBackend(t, store, base, test.requested)
			launcher := &feasibilityTestLauncher{t: t, SystemdUserExecutorLauncher: SystemdUserExecutorLauncher{meminfoPath: feasibilityMeminfo(t, test.total, test.total)}}
			b := &Backend{Store: store, Launcher: launcher}
			ctx := context.Background()
			budget, err := b.reserveExecutorResources(ctx, backend)
			var exceeds *ExecutorResourceBudgetExceedsCurrentCapacityError
			if budget != 0 || !errors.As(err, &exceeds) {
				t.Fatalf("host-infeasible explicit budget became an ordinary wait: budget=%d err=%v", budget, err)
			}
			if exceeds.BackendID != backend.BackendID || exceeds.Generation != backend.BackendGeneration || exceeds.RequestedBytes != test.requested || exceeds.HostTotalBytes != test.total || exceeds.ReserveBytes != executorControlReserveBytes || exceeds.MaximumBytes != max(0, test.total-executorControlReserveBytes) {
				t.Fatalf("host upper-bound evidence changed: %#v", exceeds)
			}
			if _, found, err := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration); err != nil || found {
				t.Fatalf("infeasible allocation entered the capacity wait ledger: found=%t err=%v", found, err)
			}
			current, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
			if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStarting || current.BackendGeneration != backend.BackendGeneration || current.ExecutorPID != 0 || current.SessionSpecJSON != backend.SessionSpecJSON || launcher.observed != 0 {
				t.Fatalf("preflight changed execution authority: %#v err=%v observations=%d", current, err, launcher.observed)
			}
			// Capacity is a current observation, not a permanent impossibility.
			// The identical durable allocation can fit after host capacity changes.
			launcher.meminfoPath = feasibilityMeminfo(t, 16<<30, 16<<30)
			budget, err = b.reserveExecutorResources(ctx, backend)
			reservation, found, readErr := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
			if err != nil || readErr != nil || !found || budget != test.requested || reservation.ReservedBytes != test.requested || reservation.RequestedBytes != test.requested || reservation.State != "reserved" {
				t.Fatalf("changed capacity did not preserve original allocation: budget=%d reservation=%#v err=%v read=%v", budget, reservation, err, readErr)
			}
		})
	}
}

func TestKernelHostFeasibilityPreservesBoundaryTransientWaitAndDefault(t *testing.T) {
	t.Run("no_host_observation_does_not_prove_infeasibility", func(t *testing.T) {
		store, base := startupBackendFixture(t)
		backend := resourceObservationBackend(t, store, base, 10<<30)
		// This custom planner supplies a generic maximum but no host-total
		// observation. It cannot justify the new host-capacity classification.
		launcher := &explicitBudgetTestLauncher{t: t}
		budget, err := (&Backend{Store: store, Launcher: launcher}).reserveExecutorResources(context.Background(), backend)
		var waiting *ExecutorResourceUnavailableError
		if budget != 0 || !errors.As(err, &waiting) || launcher.requested != 10<<30 {
			t.Fatalf("missing physical observation became a host limit: budget=%d err=%v", budget, err)
		}
		reservation, found, readErr := store.KernelResourceReservation(context.Background(), backend.BackendID, backend.BackendGeneration)
		if readErr != nil || !found || reservation.State != "waiting" || reservation.RequestedBytes != 10<<30 {
			t.Fatalf("custom planner wait changed: %#v err=%v", reservation, readErr)
		}
	})
	for _, test := range []struct {
		name                       string
		available, requested, want int64
	}{
		{"exact_explicit_maximum", 12 << 30, 9 << 30, 9 << 30},
		{"transient_explicit_shortage", 11 << 30, 9 << 30, 0},
		{"unchanged_default", 12 << 30, 0, memoryFraction(12<<30, executorMemoryTotalFraction)},
		{"transient_default_shortage", 2 << 30, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, base := startupBackendFixture(t)
			backend := resourceObservationBackend(t, store, base, test.requested)
			launcher := &feasibilityTestLauncher{t: t, SystemdUserExecutorLauncher: SystemdUserExecutorLauncher{meminfoPath: feasibilityMeminfo(t, 12<<30, test.available)}}
			budget, err := (&Backend{Store: store, Launcher: launcher}).reserveExecutorResources(context.Background(), backend)
			reservation, found, readErr := store.KernelResourceReservation(context.Background(), backend.BackendID, backend.BackendGeneration)
			if budget != test.want || readErr != nil || !found || reservation.RequestedBytes != test.requested || reservation.ReservedBytes != test.want {
				t.Fatalf("allocation contract changed: budget=%d reservation=%#v err=%v read=%v", budget, reservation, err, readErr)
			}
			if test.want == 0 {
				var waiting *ExecutorResourceUnavailableError
				if !errors.As(err, &waiting) || reservation.State != "waiting" || waiting.AvailableBytes != test.available {
					t.Fatalf("transient shortage was not retained as capacity waiting: %#v %v", reservation, err)
				}
			} else if err != nil || reservation.State != "reserved" {
				t.Fatalf("feasible allocation was rejected: %#v %v", reservation, err)
			}
		})
	}
}

func TestInfeasibleLegacyResourceWaitSettlesOnlyAfterObservedExit(t *testing.T) {
	for _, state := range []ExecutorLaunchState{ExecutorLaunchExited, ExecutorLaunchUnknown} {
		t.Run(string(state), func(t *testing.T) {
			store, base := startupBackendFixture(t)
			backend := resourceObservationBackend(t, store, base, 10<<30)
			ctx := context.Background()
			// Persist the actual legacy wait before asking the current launcher to
			// reconcile it. This neither allocates memory nor launches an executor.
			capacity := workspace.KernelResourceCapacity{MaximumBytes: 20 << 30, DefaultBytes: 2 << 30, MinimumBytes: executorMemoryFloorBytes}
			if _, admitted, err := store.AdmitKernelResources(ctx, backend, 10<<30, capacity); err != nil || admitted {
				t.Fatalf("legacy wait fixture failed: admitted=%t err=%v", admitted, err)
			}
			launcher := &feasibilityTestLauncher{t: t, state: state, SystemdUserExecutorLauncher: SystemdUserExecutorLauncher{meminfoPath: feasibilityMeminfo(t, 12<<30, 12<<30)}}
			b := &Backend{Store: store, Launcher: launcher}
			// Existing FIFO skips a demand beyond the current maximum even before
			// cleanup. A runnable successor must not be stranded behind it.
			smaller := resourceObservationBackend(t, store, backend, 1<<30)
			if budget, err := b.reserveExecutorResources(ctx, smaller); err != nil || budget != 1<<30 {
				t.Fatalf("infeasible predecessor blocked runnable allocation: budget=%d err=%v", budget, err)
			}
			handled, err := b.reconcileResourceWait(ctx, backend)
			var exceeds *ExecutorResourceBudgetExceedsCurrentCapacityError
			if !handled || !errors.As(err, &exceeds) || launcher.observed != 1 {
				t.Fatalf("legacy wait did not surface its typed capacity cause: handled=%t observations=%d err=%v", handled, launcher.observed, err)
			}
			current, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
			if err != nil || !found || current.BackendGeneration != backend.BackendGeneration || current.SessionSpecJSON != backend.SessionSpecJSON || current.ExecutorPID != 0 {
				t.Fatalf("legacy wait lost generation/allocation authority: %#v %v", current, err)
			}
			reservation, found, err := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
			if err != nil || !found || reservation.RequestedBytes != 10<<30 || reservation.ReservedBytes != 0 {
				t.Fatalf("legacy reservation evidence lost: %#v %v", reservation, err)
			}
			failure, failed, err := store.GetKernelStartupFailure(ctx, backend.BackendID, backend.BackendGeneration)
			if err != nil {
				t.Fatal(err)
			}
			if state == ExecutorLaunchExited {
				if current.State != workspace.KernelExecutionBackendStateStopped || reservation.State != "released" || !failed || failure.Stage != "launch" || failure.ExecutorInstanceID != backend.ExecutorInstanceID {
					t.Fatalf("proven unstarted generation retained a wait: backend=%#v reservation=%#v failure=%#v", current, reservation, failure)
				}
				if handled, err := b.reconcileResourceWait(ctx, current); handled || err != nil || launcher.observed != 1 {
					t.Fatalf("settled wait was not idempotent: handled=%t observations=%d err=%v", handled, launcher.observed, err)
				}
			} else if current.State != workspace.KernelExecutionBackendStateStarting || reservation.State != "waiting" || failed {
				t.Fatalf("unknown process authority was silently retired: backend=%#v reservation=%#v failure=%#v", current, reservation, failure)
			}
		})
	}
}
