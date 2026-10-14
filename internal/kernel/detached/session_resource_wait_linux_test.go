//go:build linux

package detached

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type sessionResourceWaitLauncher struct {
	available atomic.Int64
	launches  atomic.Int32
	observed  chan struct{}
	once      sync.Once
	launch    func(ExecutorLaunchRequest) error
}

func (l *sessionResourceWaitLauncher) Launch(request ExecutorLaunchRequest) error {
	l.launches.Add(1)
	if l.launch != nil {
		return l.launch(request)
	}
	return errors.New("unexpected executor launch")
}

func (*sessionResourceWaitLauncher) Observe(context.Context, string, int64) (ExecutorLaunchState, error) {
	return ExecutorLaunchUnknown, nil
}

func (l *sessionResourceWaitLauncher) ResourceCapacity(context.Context, int64) (workspace.KernelResourceCapacity, executorCapacityObservation, error) {
	l.once.Do(func() { close(l.observed) })
	available := l.available.Load()
	return workspace.KernelResourceCapacity{AvailableBytes: available, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{}}, executorCapacityObservation{AvailableBytes: available}, nil
}

func (*sessionResourceWaitLauncher) ResourceUsage(context.Context, string, int64) (int64, ExecutorLaunchState, error) {
	return 0, ExecutorLaunchUnknown, nil
}

func sessionResourceWaitFixture(t *testing.T) (*Backend, kernelruntime.SessionSpec, workspace.KernelExecutionBackend, *sessionResourceWaitLauncher) {
	t.Helper()
	store, backend := startupBackendFixture(t)
	durable, err := workspace.DecodeKernelExecutionSessionSpecV1(backend.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	spec := kernelSessionSpec(durable)
	if _, admitted, err := store.AdmitKernelResources(context.Background(), backend, 0, workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}); err != nil || admitted {
		t.Fatalf("waiting fixture admitted=%t err=%v", admitted, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := &sessionResourceWaitLauncher{observed: make(chan struct{})}
	b := &Backend{Store: store, Executable: executable, HomeDir: spec.WorkspaceDir, SocketRoot: filepath.Dir(backend.SocketPath), LogDir: filepath.Join(spec.WorkspaceDir, "logs"), Launcher: launcher, StartTimeout: 5 * time.Second}
	return b, spec, backend, launcher
}

func TestEnsureSessionResourceWaitKeepsInvocationPending(t *testing.T) {
	b, spec, backend, launcher := sessionResourceWaitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.EnsureSession(ctx, spec); done <- err }()
	select {
	case <-launcher.observed:
	case <-time.After(3 * time.Second):
		t.Fatal("actual resource planner was not reached")
	}
	select {
	case err := <-done:
		t.Fatalf("durable capacity wait escaped the original invocation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	reservation, found, err := b.Store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || reservation.State != "waiting" || launcher.launches.Load() != 0 {
		t.Fatalf("wait mutated execution: reservation=%+v found=%t launches=%d err=%v", reservation, found, launcher.launches.Load(), err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller cancellation was hidden: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capacity waiter did not stop with its caller")
	}
}

type resourceWaitRequestContext struct {
	context.Context
	request context.Context
}

func (c resourceWaitRequestContext) ResourceAdmissionWaitContext() context.Context { return c.request }

func TestResourceWaitCancellationInterruptsSQLiteConnectionWait(t *testing.T) {
	b, _, backend, _ := sessionResourceWaitFixture(t)
	repo, err := b.Store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locked, release, transactionDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		transactionDone <- repo.RunImmediate(context.Background(), func(*transcriptstore.ImmediateTransaction) error { close(locked); <-release; return nil })
	}()
	<-locked
	defer func() {
		close(release)
		if err := <-transactionDone; err != nil {
			t.Error(err)
		}
	}()
	request, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	ctx := resourceWaitRequestContext{Context: context.Background(), request: request}
	done := make(chan error, 1)
	go func() { done <- b.waitForSessionResourceCapacity(ctx, backend) }()
	select {
	case err := <-done:
		t.Fatalf("expected an occupied SQLite connection, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancelRequest()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("database wait lost caller cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete queue waiter remained blocked on the SQLite connection")
	}
}

type resourceWaitMaskedValues struct {
	context.Context
	values context.Context
}

func (c resourceWaitMaskedValues) Value(key any) any { return c.values.Value(key) }

func TestResourceWaitDeadlineNeverBecomesNilSuccess(t *testing.T) {
	live, cancelLive := context.WithCancel(context.Background())
	defer cancelLive()
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	ctx := resourceWaitMaskedValues{Context: deadline, values: live}
	if ctx.Err() != context.DeadlineExceeded || live.Err() != nil {
		t.Fatal("fixture did not retain the live lifetime behind an expired setup deadline")
	}
	if err := (&Backend{}).launchExecutorWithResourceWait(ctx, workspace.KernelExecutionBackend{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled startup was incorrectly admitted: %v", err)
	}
	queued, cleanup := resourceAdmissionContext(ctx)
	defer cleanup()
	if !errors.Is(resourceWaitContextError(queued), context.DeadlineExceeded) {
		t.Fatal("queue context lost the effective deadline")
	}
}

func TestResourceWaitRejectsDifferentImmutableAuthority(t *testing.T) {
	b, _, backend, _ := sessionResourceWaitFixture(t)
	for _, test := range []struct {
		name   string
		change func(*workspace.KernelExecutionBackend)
	}{
		{"generation", func(v *workspace.KernelExecutionBackend) { v.BackendGeneration++ }},
		{"owner", func(v *workspace.KernelExecutionBackend) { v.OwnerUserID = "other-owner" }},
		{"boot", func(v *workspace.KernelExecutionBackend) { v.MachineBootID = "other-boot" }},
		{"incarnation", func(v *workspace.KernelExecutionBackend) { v.FrameIncarnationID = "other-incarnation" }},
		{"source", func(v *workspace.KernelExecutionBackend) { v.SessionSpecSHA256 = "other-source" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected := backend
			test.change(&expected)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := b.waitForSessionResourceCapacity(ctx, expected); !errors.Is(err, workspace.ErrKernelExecutionBackendStale) {
				t.Fatalf("foreign wait authority was admitted: %v", err)
			}
		})
	}
}

func TestResourceWaitSurvivesStoreReopenWithoutAnotherReservation(t *testing.T) {
	b, spec, backend, launcher := sessionResourceWaitFixture(t)
	reopened, err := workspace.Open(filepath.Join(spec.WorkspaceDir, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := &Backend{Store: reopened, Executable: b.Executable, HomeDir: b.HomeDir, SocketRoot: b.SocketRoot, LogDir: b.LogDir, Launcher: launcher, StartTimeout: b.StartTimeout}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := restored.EnsureSession(ctx, spec); done <- err }()
	select {
	case <-launcher.observed:
	case <-time.After(3 * time.Second):
		t.Fatal("reopened resource wait was not recovered")
	}
	select {
	case err := <-done:
		t.Fatalf("reopened wait escaped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reservations, err := reopened.ListKernelResourceReservations(context.Background(), backend.MachineBootID)
	if err != nil || len(reservations) != 1 || reservations[0].BackendID != backend.BackendID || reservations[0].Generation != backend.BackendGeneration || reservations[0].State != "waiting" || launcher.launches.Load() != 0 {
		t.Fatalf("restart duplicated or launched the queued generation: reservations=%+v launches=%d err=%v", reservations, launcher.launches.Load(), err)
	}
}
