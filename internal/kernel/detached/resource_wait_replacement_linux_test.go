//go:build linux

package detached

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

type resourceReplacementLauncher struct {
	predecessorID string
	observation   ExecutorLaunchState
	launchError   error
	launches      []ExecutorLaunchRequest
	observeHook   func(context.Context) error
	capacityHook  func(context.Context) error
}

func (l *resourceReplacementLauncher) Launch(request ExecutorLaunchRequest) error {
	l.launches = append(l.launches, request)
	return l.launchError
}

func (l *resourceReplacementLauncher) Observe(ctx context.Context, id string, _ int64) (ExecutorLaunchState, error) {
	if id == l.predecessorID {
		if l.observeHook != nil {
			hook := l.observeHook
			l.observeHook = nil
			if err := hook(ctx); err != nil {
				return ExecutorLaunchUnknown, err
			}
		}
		return l.observation, nil
	}
	return ExecutorLaunchUnknown, nil
}

func (l *resourceReplacementLauncher) ResourceCapacity(ctx context.Context, _ int64) (workspace.KernelResourceCapacity, executorCapacityObservation, error) {
	if l.capacityHook != nil {
		hook := l.capacityHook
		l.capacityHook = nil
		if err := hook(ctx); err != nil {
			return workspace.KernelResourceCapacity{}, executorCapacityObservation{}, err
		}
	}
	return workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{}}, executorCapacityObservation{AvailableBytes: 2 << 30}, nil
}

func (*resourceReplacementLauncher) ResourceUsage(context.Context, string, int64) (int64, ExecutorLaunchState, error) {
	return 0, ExecutorLaunchUnknown, nil
}

func resourceReplacementFixture(t *testing.T) (*Backend, kernelruntime.SessionSpec, workspace.KernelExecutionBackend, *resourceReplacementLauncher) {
	t.Helper()
	store, previous := startupBackendFixture(t)
	durable, err := workspace.DecodeKernelExecutionSessionSpecV1(previous.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	spec := kernelSessionSpec(durable)
	reservation, admitted, err := store.AdmitKernelResources(context.Background(), previous, 0,
		workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes})
	if err != nil || admitted || reservation.State != "waiting" || reservation.ReservedBytes != 0 {
		t.Fatalf("fixture did not establish an unlaunched resource wait: %+v admitted=%t err=%v", reservation, admitted, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := &resourceReplacementLauncher{predecessorID: previous.BackendID, observation: ExecutorLaunchExited, launchError: errors.New("replacement launch boundary reached")}
	backend := &Backend{Store: store, Executable: executable, HomeDir: spec.WorkspaceDir,
		SocketRoot: filepath.Dir(previous.SocketPath), LogDir: filepath.Join(spec.WorkspaceDir, "logs"),
		Launcher: launcher, StartTimeout: 5 * time.Second}
	return backend, spec, previous, launcher
}

func TestResourceWaitReplacementRetiresNeverLaunchedPredecessor(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spec.ResourceMemoryBytes = 1 << 30
	if _, found, err := b.Store.FindKernelExecutionBackendForSession(ctx, durableSessionSpec(spec)); err != nil || found {
		t.Fatalf("changed allocation unexpectedly matched the old immutable specification: found=%t err=%v", found, err)
	}
	if _, err := b.EnsureSession(ctx, spec); !errors.Is(err, launcher.launchError) {
		t.Fatalf("proven unlaunched predecessor blocked the changed allocation before launch: %v", err)
	}
	retired, found, err := b.Store.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || retired.State != workspace.KernelExecutionBackendStateStopped ||
		retired.SessionSpecSHA256 != previous.SessionSpecSHA256 || retired.ExecutorPID != 0 || retired.WorkerPID != 0 {
		t.Fatalf("old generation was not retired without mutating its input: %+v found=%t err=%v", retired, found, err)
	}
	reservation, found, err := b.Store.KernelResourceReservation(ctx, previous.BackendID, previous.BackendGeneration)
	if err != nil || !found || reservation.State != "released" || reservation.RequestedBytes != 0 || reservation.ReservedBytes != 0 {
		t.Fatalf("old resource wait lost its immutable demand or release fence: %+v found=%t err=%v", reservation, found, err)
	}
	replacement, found, err := b.Store.FindKernelExecutionBackendForSession(ctx, durableSessionSpec(spec))
	if err != nil || !found || replacement.BackendID == previous.BackendID || replacement.KernelGeneration != previous.KernelGeneration+1 ||
		len(launcher.launches) != 1 || launcher.launches[0].BackendID != replacement.BackendID || launcher.launches[0].MemoryMaxBytes != spec.ResourceMemoryBytes {
		t.Fatalf("replacement did not preserve the new exact demand: backend=%+v launches=%+v found=%t err=%v", replacement, launcher.launches, found, err)
	}
	if _, found, err := b.Store.GetKernelStartupFailure(ctx, previous.BackendID, previous.BackendGeneration); err != nil || found {
		t.Fatalf("never-launched retirement fabricated a startup failure: found=%t err=%v", found, err)
	}
}

func TestResourceWaitReplacementResumesReleasedFence(t *testing.T) {
	b, spec, previous, launcher := resourceReplacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cancelled, err := b.Store.CancelWaitingKernelResources(ctx, previous.BackendID, previous.BackendGeneration); err != nil || !cancelled {
		t.Fatalf("could not establish the interruption between cancellation and finish: cancelled=%t err=%v", cancelled, err)
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
	if _, admitted, err := reopened.AdmitKernelResources(ctx, previous, 0,
		workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}); !errors.Is(err, workspace.ErrKernelExecutionBackendStale) || admitted {
		t.Fatalf("released generation regained physical launch authority: admitted=%t err=%v", admitted, err)
	}
	spec.ResourceMemoryBytes = 1 << 30
	if _, err := b.EnsureSession(ctx, spec); !errors.Is(err, launcher.launchError) {
		t.Fatalf("durable release fence could not finish replacement after Store reopen: %v", err)
	}
	current, found, err := reopened.GetKernelExecutionBackend(ctx, previous.BackendID)
	if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStopped || current.SessionSpecSHA256 != previous.SessionSpecSHA256 || len(launcher.launches) != 1 {
		t.Fatalf("released predecessor did not finish exactly once: %+v launches=%+v found=%t err=%v", current, launcher.launches, found, err)
	}
	if _, found, err := reopened.GetKernelStartupFailure(ctx, previous.BackendID, previous.BackendGeneration); err != nil || found {
		t.Fatalf("release-fence recovery fabricated a startup failure: found=%t err=%v", found, err)
	}
}
