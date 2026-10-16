//go:build linux

package detached

import (
	"errors"
	"reflect"
	"testing"
)

func TestReservedDefaultKernelBudgetRechecksProvenHostMaximum(t *testing.T) {
	store, base := startupBackendFixture(t)
	backend := resourceObservationBackend(t, store, base, 0)
	ctx := t.Context()
	launcher := &feasibilityTestLauncher{t: t, SystemdUserExecutorLauncher: SystemdUserExecutorLauncher{
		meminfoPath: feasibilityMeminfo(t, 20<<30, 20<<30),
	}}
	b := &Backend{Store: store, Launcher: launcher}
	initialBudget, err := b.reserveExecutorResources(ctx, backend)
	if err != nil || initialBudget != 9<<30 {
		t.Fatalf("initial production-planner default was not reserved: budget=%d err=%v", initialBudget, err)
	}
	original, found, err := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || original.RequestedBytes != 0 || original.ReservedBytes != initialBudget || original.State != "reserved" {
		t.Fatalf("initial durable default reservation is invalid: %+v found=%t err=%v", original, found, err)
	}
	originalBackend, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
	if err != nil || !found || originalBackend.ExecutorPID != 0 {
		t.Fatalf("initial unlaunched backend is unavailable: %+v found=%t err=%v", originalBackend, found, err)
	}
	for _, tc := range []struct {
		name               string
		total, available   int64
		hardLimit, unknown bool
	}{
		{"host_shrink_below_reserved_above_floor", 10 << 30, 10 << 30, true, false},
		{"restored_host_replays_original_reservation", 20 << 30, 20 << 30, false, false},
		{"same_host_temporary_pressure_is_not_hard_limit", 20 << 30, 2 << 30, false, false},
		{"no_host_total_does_not_prove_hard_limit", 0, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unknown {
				// This existing planner has no host-total evidence and a smaller
				// current/default capacity; neither may rewrite an admitted default.
				b.Launcher = resourceNotificationLauncher{launch: func(ExecutorLaunchRequest) error {
					t.Fatal("reserved-budget preflight must not launch computation")
					return nil
				}}
			} else {
				launcher.meminfoPath = feasibilityMeminfo(t, tc.total, tc.available)
				b.Launcher = launcher
			}
			budget, reserveErr := b.reserveExecutorResources(ctx, backend)
			if tc.hardLimit {
				var exceeds *ExecutorResourceBudgetExceedsCurrentCapacityError
				if budget != 0 || !errors.As(reserveErr, &exceeds) {
					t.Errorf("reserved default survived a proven smaller host maximum: budget=%d err=%v", budget, reserveErr)
				} else if exceeds.BackendID != backend.BackendID || exceeds.Generation != backend.BackendGeneration ||
					exceeds.RequestedBytes != 0 || exceeds.HostTotalBytes != tc.total || exceeds.MaximumBytes != 6<<30 ||
					exceeds.MaximumBytes <= executorMemoryFloorBytes || exceeds.MaximumBytes >= original.ReservedBytes {
					t.Errorf("replay rejection lost original demand/current hard evidence: %+v", exceeds)
				}
			} else if reserveErr != nil || budget != initialBudget {
				t.Errorf("replay rewrote an admitted default without proven infeasibility: budget=%d want=%d err=%v", budget, initialBudget, reserveErr)
			}
			after, found, readErr := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
			if readErr != nil || !found || after != original {
				t.Errorf("preflight mutated the original reserved/requested bytes or state: %+v want=%+v found=%t err=%v", after, original, found, readErr)
			}
			current, found, readErr := store.GetKernelExecutionBackend(ctx, backend.BackendID)
			if readErr != nil || !found || !reflect.DeepEqual(current, originalBackend) || launcher.observed != 0 {
				t.Errorf("preflight changed session/generation/execution authority: %+v want=%+v found=%t observed=%d err=%v", current, originalBackend, found, launcher.observed, readErr)
			}
		})
	}
}
