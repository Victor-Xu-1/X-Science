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

func resourceObservationMeminfo(t *testing.T, available int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", (10<<30)/1024, available/1024)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resourceObservationBackend(t *testing.T, store *workspace.Store, base workspace.KernelExecutionBackend, requested int64) workspace.KernelExecutionBackend {
	t.Helper()
	spec, err := workspace.DecodeKernelExecutionSessionSpecV1(base.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	spec.KernelID += "-observation"
	spec.ResourceMemoryBytes = requested
	backend, err := store.CreateKernelExecutionBackend(context.Background(), workspace.CreateKernelExecutionBackendInput{
		BackendID: base.BackendID + "-observation", OwnerUserID: base.OwnerUserID, ProjectID: base.ProjectID,
		RootFrameID: base.RootFrameID, RootFrameIncarnationID: base.RootFrameIncarnationID, FrameID: base.FrameID,
		FrameIncarnationID: base.FrameIncarnationID, KernelID: spec.KernelID, KernelGeneration: 1, SessionSpec: spec,
		ExecutorInstanceID: base.ExecutorInstanceID + "-observation", MachineBootID: base.MachineBootID,
		BackendGeneration: 1, SocketPath: base.SocketPath + "-observation",
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func TestExecutorCapacityRejectionReportsObservedMemory(t *testing.T) {
	for _, test := range []struct {
		name                          string
		available, requested, reserve int64
		reserved                      bool
	}{
		{"default_below", 2 << 30, 0, 3 << 30, false},
		{"default_equal", 3 << 30, 0, 3 << 30, false},
		{"default_above", (3 << 30) + (512 << 20), 0, 3 << 30, false},
		{"explicit_below", 2 << 30, 2 << 30, 3 << 30, false},
		{"explicit_equal", 3 << 30, 2 << 30, 3 << 30, false},
		{"explicit_above", 4 << 30, 2 << 30, 3 << 30, false},
		{"default_outstanding_reservation", 4 << 30, 0, 3 << 30, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, base := startupBackendFixture(t)
			if test.reserved {
				capacity := workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}
				if _, admitted, err := store.AdmitKernelResources(context.Background(), base, 0, capacity); err != nil || !admitted {
					t.Fatal("fixture reservation was not admitted", admitted, err)
				}
			}
			backend := resourceObservationBackend(t, store, base, test.requested)
			// No systemctl authority is supplied: usage stays unknown and the
			// outstanding promise remains conservative, without host execution.
			launcher := &SystemdUserExecutorLauncher{meminfoPath: resourceObservationMeminfo(t, test.available)}
			budget, err := (&Backend{Store: store, Launcher: launcher}).reserveExecutorResources(context.Background(), backend)
			var unavailable *ExecutorResourceUnavailableError
			if budget != 0 || !errors.As(err, &unavailable) {
				t.Fatalf("expected unchanged capacity rejection, budget=%d err=%v", budget, err)
			}
			if unavailable.AvailableBytes != test.available || unavailable.ReserveBytes != test.reserve || unavailable.RequiredBytes != max(test.requested, executorMemoryFloorBytes) {
				t.Errorf("observation changed: %#v, want raw=%d reserve=%d requested=%d", unavailable, test.available, test.reserve, test.requested)
			}
			reservation, found, err := store.KernelResourceReservation(context.Background(), backend.BackendID, backend.BackendGeneration)
			if err != nil || !found || reservation.State != "waiting" || reservation.RequestedBytes != test.requested {
				t.Fatalf("rejection lost original durable wait: %#v found=%t err=%v", reservation, found, err)
			}
			if test.reserved {
				prior, found, err := store.KernelResourceReservation(context.Background(), base.BackendID, base.BackendGeneration)
				if err != nil || !found || prior.State != "reserved" || prior.ReservedBytes != 1<<30 {
					t.Fatalf("capacity reporting changed outstanding promise: %#v found=%t err=%v", prior, found, err)
				}
			}
		})
	}
}
