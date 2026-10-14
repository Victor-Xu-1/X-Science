package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
