//go:build linux

package detached

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestExplicitExecutorBudgetUsesLargeHostCapacityWithoutDefaultShareCeiling(t *testing.T) {
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal: 134217728 kB\nMemAvailable: 117440512 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &SystemdUserExecutorLauncher{meminfoPath: meminfo}
	capacity, reserve, err := launcher.ResourceCapacity(context.Background(), 100<<30)
	if err != nil || reserve != executorControlReserveBytes || capacity.AvailableBytes < 100<<30 {
		t.Fatalf("explicit large-host capacity=%#v reserve=%d err=%v", capacity, reserve, err)
	}
	defaultCapacity, _, err := launcher.ResourceCapacity(context.Background(), 0)
	if err != nil || defaultCapacity.AvailableBytes >= capacity.AvailableBytes {
		t.Fatal("conservative default was not kept separate from explicit allocation")
	}
}

func cloneResourceBackend(t *testing.T, store *workspace.Store, base workspace.KernelExecutionBackend, suffix string) workspace.KernelExecutionBackend {
	t.Helper()
	spec, err := workspace.DecodeKernelExecutionSessionSpecV1(base.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	spec.KernelID += "-" + suffix
	backend, err := store.CreateKernelExecutionBackend(context.Background(), workspace.CreateKernelExecutionBackendInput{BackendID: base.BackendID + "-" + suffix, OwnerUserID: base.OwnerUserID, ProjectID: base.ProjectID, RootFrameID: base.RootFrameID, RootFrameIncarnationID: base.RootFrameIncarnationID, FrameID: base.FrameID, FrameIncarnationID: base.FrameIncarnationID, KernelID: spec.KernelID, KernelGeneration: 1, SessionSpec: spec, ExecutorInstanceID: base.ExecutorInstanceID + "-" + suffix, MachineBootID: base.MachineBootID, BackendGeneration: 1, SocketPath: base.SocketPath + suffix})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func TestKernelResourceReservationsSerializeConcurrentPromisesAndPersistWaiting(t *testing.T) {
	store, base := startupBackendFixture(t)
	ctx := context.Background()
	capacity := workspace.KernelResourceCapacity{AvailableBytes: 2500, DefaultBytes: 1000, MinimumBytes: 100}
	var backends []workspace.KernelExecutionBackend
	for i := range 12 {
		backends = append(backends, cloneResourceBackend(t, store, base, fmt.Sprint(i)))
	}
	var workers sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	var failures []error
	for _, backend := range backends {
		workers.Go(func() {
			_, accepted, err := store.AdmitKernelResources(ctx, backend, 1000, capacity)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
			}
			if accepted {
				admitted++
			}
		})
	}
	workers.Wait()
	if len(failures) != 0 || admitted != 2 {
		t.Fatalf("admitted=%d failures=%v", admitted, failures)
	}
	items, err := store.ListKernelResourceReservations(ctx, base.MachineBootID)
	if err != nil {
		t.Fatal(err)
	}
	var promised int64
	waiting := 0
	for _, item := range items {
		if item.State == "reserved" {
			promised += item.ReservedBytes
		} else if item.State == "waiting" {
			waiting++
		}
	}
	if promised != 2000 || waiting != 10 {
		t.Fatalf("promised=%d waiting=%d", promised, waiting)
	}
	// Reconstructing a Store/Backend does not erase the serialized promise.
	if replay, accepted, err := store.AdmitKernelResources(ctx, backends[0], 1001, capacity); err == nil {
		t.Fatalf("demand changed within a generation: %#v %t", replay, accepted)
	}
}

func TestKernelResourceAdmissionDoesNotDoubleCountObservedUsage(t *testing.T) {
	store, first := startupBackendFixture(t)
	ctx := context.Background()
	second := cloneResourceBackend(t, store, first, "second")
	capacity := workspace.KernelResourceCapacity{AvailableBytes: 2000, DefaultBytes: 1500, MinimumBytes: 100}
	if _, ok, err := store.AdmitKernelResources(ctx, first, 1500, capacity); err != nil || !ok {
		t.Fatal(ok, err)
	}
	capacity.AvailableBytes = 1000 // First reservation actually uses 1000 bytes.
	capacity.Usage = map[string]int64{workspace.KernelResourceUsageKey(first.BackendID, first.BackendGeneration): 1000}
	if _, ok, err := store.AdmitKernelResources(ctx, second, 500, capacity); err != nil || !ok {
		t.Fatal("already-used memory counted twice", ok, err)
	}
}

func TestKernelResourceWaitingRetainsOriginalGenerationAndDoesNotInventAnExit(t *testing.T) {
	store, backend := startupBackendFixture(t)
	ctx := context.Background()
	capacity := workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1000, MinimumBytes: 100}
	if reservation, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || ok || reservation.State != "waiting" {
		t.Fatal(reservation, ok, err)
	}
	if err := (&Backend{Store: store}).settleExitedStartup(ctx, backend); err != errStartupStillOwned {
		t.Fatalf("capacity wait became a process failure: %v", err)
	}
	capacity.AvailableBytes = 1000
	reservation, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity)
	if err != nil || !ok || reservation.Generation != backend.BackendGeneration {
		t.Fatal("capacity recovery replaced original generation", reservation, ok, err)
	}
}
