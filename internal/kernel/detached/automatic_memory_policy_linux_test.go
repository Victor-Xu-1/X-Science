//go:build linux

package detached

import (
	"context"
	"errors"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestAutomaticExecutorMemoryPolicyPreservesPhysicalCapacity(t *testing.T) {
	const floor = int64(768 << 20)
	backend := workspace.KernelExecutionBackend{BackendID: "kernel-backend-policy", BackendGeneration: 1}
	for _, test := range []struct {
		name                                                       string
		total, available, requested, reserve, preference, fallback int64
		infeasible, unavailable                                    bool
	}{
		{name: "healthy_2GiB", total: 2 << 30, available: 2 << 30, reserve: (2 << 30) * 40 / 100, preference: (2 << 30) * 45 / 100, fallback: (2 << 30) * 45 / 100},
		{name: "healthy_4GiB", total: 4 << 30, available: 4 << 30, reserve: (4 << 30) * 40 / 100, preference: (4 << 30) * 45 / 100, fallback: (4 << 30) * 45 / 100},
		{name: "healthy_12GiB", total: 12 << 30, available: 12 << 30, reserve: 3 << 30, preference: (12 << 30) * 45 / 100, fallback: (12 << 30) * 45 / 100},
		{name: "healthy_64GiB", total: 64 << 30, available: 64 << 30, reserve: 3 << 30, preference: (64 << 30) * 45 / 100, fallback: (64 << 30) * 45 / 100},
		{name: "exact_runtime_floor", total: 1280 << 20, available: 1280 << 20, reserve: 512 << 20, preference: floor, fallback: floor},
		{name: "one_KiB_below_runtime_floor", total: (1280 << 20) - 1024, available: (1280 << 20) - 1024, reserve: ((1280 << 20) - 1024) * 40 / 100, preference: floor, infeasible: true, unavailable: true},
		{name: "low_current_availability", total: 12 << 30, available: 2 << 30, reserve: 3 << 30, preference: (12 << 30) * 45 / 100, unavailable: true},
		{name: "current_availability_clamps_preference", total: 12 << 30, available: 4 << 30, reserve: 3 << 30, preference: (12 << 30) * 45 / 100, fallback: 1 << 30},
		{name: "explicit_large_request_retains_fixed_reserve", total: 128 << 30, available: 112 << 30, requested: 100 << 30, reserve: 3 << 30, preference: (128 << 30) * 45 / 100},
		{name: "explicit_small_host_retains_fixed_reserve", total: 2 << 30, available: 2 << 30, requested: floor, reserve: 3 << 30, preference: (2 << 30) * 45 / 100, infeasible: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := &SystemdUserExecutorLauncher{meminfoPath: feasibilityMeminfo(t, test.total, test.available)}
			capacity, observation, err := launcher.ResourceCapacity(context.Background(), test.requested)
			if err != nil {
				t.Fatal(err)
			}
			if observation.HostTotalBytes != test.total || observation.AvailableBytes != test.available || observation.ReserveBytes != test.reserve {
				t.Errorf("observation=%+v; want total=%d available=%d reserve=%d", observation, test.total, test.available, test.reserve)
			}
			if capacity.MaximumBytes != max(0, test.total-test.reserve) || capacity.AvailableBytes != max(0, test.available-test.reserve) || capacity.DefaultBytes != test.preference || capacity.MinimumBytes != floor {
				t.Errorf("capacity=%+v; want maximum=%d available=%d preference=%d floor=%d", capacity, max(0, test.total-test.reserve), max(0, test.available-test.reserve), test.preference, floor)
			}
			err = checkExecutorHostCapacity(backend, test.requested, capacity.MinimumBytes, 0, observation)
			var exceeds *ExecutorResourceBudgetExceedsCurrentCapacityError
			if test.infeasible {
				if !errors.As(err, &exceeds) || exceeds.RequestedBytes != test.requested || exceeds.ReserveBytes != test.reserve || exceeds.MaximumBytes != max(0, test.total-test.reserve) {
					t.Errorf("host-infeasible demand lost its original request or bound: %+v", err)
				}
			} else if err != nil {
				t.Errorf("viable physical demand was rejected: %v", err)
			}
			if test.requested != 0 {
				return // A positive admitted launch budget bypasses the fallback.
			}
			budget, err := executorMemoryBudget(launcher.meminfoPath)
			var unavailable *ExecutorResourceUnavailableError
			if test.unavailable {
				if budget != 0 || !errors.As(err, &unavailable) || unavailable.AvailableBytes != test.available || unavailable.ReserveBytes != test.reserve || unavailable.RequiredBytes != floor {
					t.Errorf("unavailable fallback invented capacity or lost evidence: budget=%d err=%v", budget, err)
				}
			} else if err != nil || budget != test.fallback || budget < floor || budget > capacity.AvailableBytes || budget > capacity.MaximumBytes {
				t.Errorf("fallback and planner disagree: budget=%d capacity=%+v err=%v", budget, capacity, err)
			}
		})
	}
	for _, test := range []struct {
		name     string
		total    int64
		admitted int64
	}{
		{name: "missing_host_observation_does_not_prove_infeasibility", admitted: 64 << 30},
		{name: "admitted_allocation_is_checked_against_physical_bound", total: 12 << 30, admitted: 10 << 30},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := executorCapacityObservation{HostTotalBytes: test.total, ReserveBytes: 3 << 30}
			err := checkExecutorHostCapacity(backend, 0, floor, test.admitted, observation)
			var exceeds *ExecutorResourceBudgetExceedsCurrentCapacityError
			if test.total == 0 {
				if err != nil {
					t.Errorf("missing physical observation was invented: %v", err)
				}
			} else if !errors.As(err, &exceeds) || exceeds.RequestedBytes != 0 || exceeds.ReservedBytes != test.admitted || exceeds.MaximumBytes != 9<<30 {
				t.Errorf("admitted allocation escaped the physical bound: %v", err)
			}
		})
	}
}
