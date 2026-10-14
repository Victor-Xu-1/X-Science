package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/persistence/workspace"
)

func registerKernelChildShutdownFixture(t *testing.T, owner *Server, frameID string, cancel context.CancelFunc, done chan struct{}) {
	t.Helper()
	key := kernelChildRunKey{server: owner, frameID: frameID}
	run := &kernelChildRun{cancel: cancel, done: done}
	if _, loaded := kernelChildRuns.LoadOrStore(key, run); loaded {
		t.Fatal("child shutdown fixture already exists")
	}
	t.Cleanup(func() { kernelChildRuns.CompareAndDelete(key, run) })
}

func TestKernelChildShutdownCancelsOnlyOwningServer(t *testing.T) {
	owner, other := &Server{}, &Server{}
	done := make(chan struct{})
	close(done)
	var ownedCancellations, foreignCancellations atomic.Int32
	registerKernelChildShutdownFixture(t, owner, "owned", func() { ownedCancellations.Add(1) }, done)
	registerKernelChildShutdownFixture(t, other, "foreign", func() { foreignCancellations.Add(1) }, done)
	if err := owner.cancelAllKernelChildRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ownedCancellations.Load() != 1 || foreignCancellations.Load() != 0 {
		t.Fatalf("shutdown crossed server ownership: owned=%d foreign=%d", ownedCancellations.Load(), foreignCancellations.Load())
	}
}

func TestKernelChildShutdownCancelsAllOwnedBeforeJoinDeadline(t *testing.T) {
	for _, failure := range []string{"cancelled", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			owner, other := &Server{}, &Server{}
			var first, second, foreign atomic.Int32
			registerKernelChildShutdownFixture(t, owner, "owned-first", func() { first.Add(1) }, make(chan struct{}))
			registerKernelChildShutdownFixture(t, owner, "owned-second", func() { second.Add(1) }, make(chan struct{}))
			registerKernelChildShutdownFixture(t, other, "foreign", func() { foreign.Add(1) }, make(chan struct{}))
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if failure == "deadline" {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			defer cancel()
			err := owner.cancelAllKernelChildRuns(ctx)
			if !errors.Is(err, want) {
				t.Fatalf("join error=%v, want %v", err, want)
			}
			if first.Load() != 1 || second.Load() != 1 || foreign.Load() != 0 {
				t.Fatalf("join failure prevented owned cancellation or touched foreign child: first=%d second=%d foreign=%d", first.Load(), second.Load(), foreign.Load())
			}
		})
	}
}

func TestKernelChildShutdownWaitsForOwnedSettlement(t *testing.T) {
	owner, other := &Server{}, &Server{}
	ownedDone, cancellationObserved := make(chan struct{}), make(chan struct{})
	var finishOnce, cancelOnce sync.Once
	t.Cleanup(func() { finishOnce.Do(func() { close(ownedDone) }) })
	foreignDone := make(chan struct{})
	close(foreignDone)
	var foreignCancellations atomic.Int32
	registerKernelChildShutdownFixture(t, owner, "owned-settling", func() { cancelOnce.Do(func() { close(cancellationObserved) }) }, ownedDone)
	registerKernelChildShutdownFixture(t, other, "foreign-settled", func() { foreignCancellations.Add(1) }, foreignDone)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- owner.cancelAllKernelChildRuns(ctx) }()
	select {
	case <-cancellationObserved:
	case <-ctx.Done():
		t.Fatal("owned child did not receive cancellation")
	}
	select {
	case err := <-result:
		t.Fatalf("shutdown returned before owned settlement: %v", err)
	default:
	}
	finishOnce.Do(func() { close(ownedDone) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not join completed owned settlement")
	}
	if foreignCancellations.Load() != 0 {
		t.Fatal("joining the owned child cancelled another server's child")
	}
}

func TestKernelChildGateCancellationReleasesOnlyItsRegistration(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	children, err := fixture.store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: fixture.stream.FrameID, OwnerUserID: fixture.stream.OwnerID, ToolUseID: "controlled-gate-shutdown",
		Requests: []workspace.KernelDelegateRequest{{Task: "cancel before execution"}, {Task: "preserve replacement registration"}},
	})
	if err != nil || len(children) != 2 {
		t.Fatalf("create controlled children: count=%d err=%v", len(children), err)
	}
	owner := &Server{workspaceStore: fixture.store}
	for index, replace := range []bool{false, true} {
		child := children[index]
		// No receiver ever releases this gate, so only the real cancellation
		// branch can run. No model/provider or scientific execution is reached.
		owner.startKernelChildRun(child, make(chan struct{}))
		key := kernelChildRunKey{server: owner, frameID: child.FrameID}
		registered, found := kernelChildRuns.Load(key)
		if !found {
			t.Fatal("child was not registered before waiting for its gate")
		}
		run := registered.(*kernelChildRun)
		replacement := &kernelChildRun{}
		if replace {
			kernelChildRuns.Store(key, replacement)
			t.Cleanup(func() { kernelChildRuns.CompareAndDelete(key, replacement) })
		}
		run.cancel()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		select {
		case <-run.done:
		case <-ctx.Done():
			cancel()
			t.Fatal("cancelled gate waiter did not finish")
		}
		cancel()
		actual, remains := kernelChildRuns.Load(key)
		if replace && (!remains || actual != replacement) || !replace && remains {
			t.Fatalf("gate finalizer removed the wrong registration: replace=%t remains=%t", replace, remains)
		}
		persisted, found, err := fixture.store.GetKernelSupervisedChild(t.Context(), child.ParentFrameID, child.FrameID, child.OwnerUserID)
		if err != nil || !found || persisted.Status != "cancelled" {
			t.Fatalf("gate cancellation lost real settlement: found=%t status=%q err=%v", found, persisted.Status, err)
		}
	}
}
