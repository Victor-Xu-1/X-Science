//go:build linux

package detached

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

type historicalHandoffFixture struct {
	backend     *Backend
	spec        kernelruntime.SessionSpec
	old         workspace.KernelExecutionBackend
	latest      workspace.KernelExecutionBackend
	launch      *historicalHandoffLauncher
	closes      atomic.Int32
	beforeClose func(workspace.KernelExecutionBackend) error
}

type historicalHandoffLauncher struct {
	fixture  *historicalHandoffFixture
	launch   chan ExecutorLaunchRequest
	err      error
	onLaunch func(ExecutorLaunchRequest) error
}

func (l *historicalHandoffLauncher) Launch(request ExecutorLaunchRequest) error {
	l.launch <- request
	if l.onLaunch != nil {
		return l.onLaunch(request)
	}
	return l.err
}

func (l *historicalHandoffLauncher) Observe(context.Context, string, int64) (ExecutorLaunchState, error) {
	return ExecutorLaunchExited, nil // No fixture executor is ever launched.
}

func (l *historicalHandoffLauncher) ResourceCapacity(context.Context, int64) (workspace.KernelResourceCapacity, executorCapacityObservation, error) {
	return workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 2 << 30,
		MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{}}, executorCapacityObservation{AvailableBytes: 2 << 30}, nil
}

func (l *historicalHandoffLauncher) ResourceUsage(ctx context.Context, id string, generation int64) (int64, ExecutorLaunchState, error) {
	current, found, err := l.fixture.backend.Store.GetKernelExecutionBackend(ctx, id)
	if err != nil || !found || current.BackendGeneration != generation {
		return 0, ExecutorLaunchUnknown, err
	}
	if current.State == workspace.KernelExecutionBackendStateStopped {
		return 0, ExecutorLaunchExited, nil
	}
	return 0, ExecutorLaunchAlive, nil
}

func newHistoricalHandoffFixture(t *testing.T, persistedWait bool) *historicalHandoffFixture {
	return historicalHandoffFixtureWithLiveLatest(t, persistedWait, false)
}

func historicalHandoffFixtureWithLiveLatest(t *testing.T, persistedWait, liveLatest bool) *historicalHandoffFixture {
	t.Helper()
	store, initial := startupBackendFixture(t)
	durable, err := workspace.DecodeKernelExecutionSessionSpecV1(initial.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	f := &historicalHandoffFixture{spec: kernelSessionSpec(durable)}
	f.spec.ResourceMemoryBytes = 1 << 30
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.backend = &Backend{Store: store, Executable: executable, HomeDir: f.spec.WorkspaceDir,
		SocketRoot: filepath.Dir(initial.SocketPath), LogDir: filepath.Join(f.spec.WorkspaceDir, "logs"), StartTimeout: 5 * time.Second}
	f.launch = &historicalHandoffLauncher{fixture: f, launch: make(chan ExecutorLaunchRequest, 4), err: errors.New("handoff reached authorized launch")}
	f.backend.Launcher = f.launch
	finishHistoricalFixtureBackend(t, store, initial)
	boot, err := machineBootID()
	if err != nil {
		t.Fatal(err)
	}
	create := func(id string, generation int64, spec kernelruntime.SessionSpec) workspace.KernelExecutionBackend {
		t.Helper()
		backend, err := store.CreateKernelExecutionBackend(t.Context(), workspace.CreateKernelExecutionBackendInput{
			BackendID: id, OwnerUserID: spec.OwnerID, ProjectID: spec.ProjectID,
			RootFrameID: spec.RootFrameID, RootFrameIncarnationID: spec.RootFrameIncarnationID,
			FrameID: spec.FrameID, FrameIncarnationID: spec.FrameIncarnationID,
			KernelID: spec.KernelID, KernelGeneration: generation, SessionSpec: durableSessionSpec(spec),
			ExecutorInstanceID: "instance-" + id, MachineBootID: boot,
			SocketPath: filepath.Join(f.backend.SocketRoot, id+".sock"), BackendGeneration: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return backend
	}
	// Persist A(explicit), B(AUTO), and optionally an already-recreated A wait.
	// These are real immutable Store documents; the launcher never runs code.
	f.old = create("historical-explicit", 2, f.spec)
	finishHistoricalFixtureBackend(t, store, f.old)
	auto := f.spec
	auto.ResourceMemoryBytes = 0
	f.latest = create("newer-auto", 3, auto)
	reservation, admitted, err := store.AdmitKernelResources(t.Context(), f.latest, 0,
		workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 2 << 30, MinimumBytes: executorMemoryFloorBytes})
	if err != nil || !admitted || reservation.ReservedBytes != 2<<30 {
		t.Fatalf("AUTO fixture admission: %+v admitted=%t err=%v", reservation, admitted, err)
	}
	pid, ticks := int64(1<<30), int64(1)
	if liveLatest {
		pid = int64(os.Getpid())
		ticks, err = kernelruntime.CurrentProcessStartTicks()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.latest, err = store.ActivateKernelExecutionBackend(t.Context(), workspace.ActivateKernelExecutionBackendInput{
		BackendID: f.latest.BackendID, BackendGeneration: f.latest.BackendGeneration, ExecutorInstanceID: f.latest.ExecutorInstanceID,
		ExecutorPID: pid, ExecutorPIDStartTicks: ticks, WorkerPID: pid, WorkerPIDStartTicks: ticks,
		WorkerPGID: pid, HeartbeatSequence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if persistedWait {
		f.old, err = store.RecreateKernelExecutionBackend(t.Context(), workspace.RecreateKernelExecutionBackendInput{
			BackendID: f.old.BackendID, ExecutorInstanceID: "old-recreated", MachineBootID: boot,
			SocketPath: f.old.SocketPath, KernelID: f.old.KernelID, KernelGeneration: f.old.KernelGeneration,
			SessionSpec: durableSessionSpec(f.spec),
		})
		if err != nil {
			t.Fatal(err)
		}
		reservation, admitted, err = store.AdmitKernelResources(t.Context(), f.old, f.spec.ResourceMemoryBytes,
			workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 2 << 30, MinimumBytes: executorMemoryFloorBytes})
		if err != nil || admitted || reservation.State != "waiting" || reservation.ReservedBytes != 0 {
			t.Fatalf("historical persisted wait: %+v admitted=%t err=%v", reservation, admitted, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := workspace.Open(filepath.Join(f.spec.WorkspaceDir, "workspace.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		f.backend.Store = reopened
	}
	f.serveClose(t, f.latest)
	return f
}

func finishHistoricalFixtureBackend(t *testing.T, store *workspace.Store, backend workspace.KernelExecutionBackend) {
	t.Helper()
	if backend.State == workspace.KernelExecutionBackendStateStarting {
		spec, err := workspace.DecodeKernelExecutionSessionSpecV1(backend.SessionSpecJSON)
		if err != nil {
			t.Fatal(err)
		}
		if _, admitted, err := store.AdmitKernelResources(t.Context(), backend, spec.ResourceMemoryBytes,
			workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}); err != nil || admitted {
			t.Fatalf("seed never-launched wait: admitted=%t err=%v", admitted, err)
		}
		if cancelled, err := store.CancelWaitingKernelResources(t.Context(), backend.BackendID, backend.BackendGeneration); err != nil || !cancelled {
			t.Fatalf("seed released-zero terminal proof: cancelled=%t err=%v", cancelled, err)
		}
	}
	if _, err := store.FinishKernelExecutionBackend(t.Context(), workspace.FinishKernelExecutionBackendInput{
		BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *historicalHandoffFixture) serveClose(t *testing.T, backend workspace.KernelExecutionBackend) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(backend.SocketPath), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: backend.SocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backend.SocketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		frame, err := readBoundedCommandFrame(connection, maxCommandRequestBytes)
		if err != nil {
			return
		}
		request, err := DecodeCommandRequest(frame)
		executor := &Executor{Store: f.backend.Store}
		if err != nil || request.Command != CommandClose || request.BackendID != backend.BackendID ||
			request.BackendGeneration != backend.BackendGeneration || executor.validateController(context.Background(), request) != nil {
			return
		}
		current, found, err := f.backend.Store.GetKernelExecutionBackend(context.Background(), backend.BackendID)
		if err != nil || !found || current.State != workspace.KernelExecutionBackendStateDraining {
			return
		}
		if f.beforeClose != nil {
			if err := f.beforeClose(current); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := f.backend.Store.FinishKernelExecutionBackend(context.Background(), workspace.FinishKernelExecutionBackendInput{
			BackendID: current.BackendID, BackendGeneration: current.BackendGeneration, ExecutorInstanceID: current.ExecutorInstanceID,
		}); err != nil {
			t.Error(err)
			return
		}
		if err := f.backend.Store.ReleaseKernelResources(context.Background(), current.BackendID, current.BackendGeneration, current.MachineBootID); err != nil {
			t.Error(err)
			return
		}
		f.closes.Add(1)
		response, err := EncodeCommandResponse(commandResponseOK(request.RequestID))
		if err == nil {
			_, _ = connection.Write(response)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("controlled close fixture did not stop")
		}
	})
}
