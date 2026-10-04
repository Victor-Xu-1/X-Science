package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

type leaseTestRepository struct {
	*workspace.Store
	failOnce atomic.Bool
	renewals atomic.Int32
	renewed  chan struct{}
}

func (r *leaseTestRepository) RenewOutboxClaim(ctx context.Context, id, token string, lease time.Duration) error {
	if r.failOnce.CompareAndSwap(true, false) {
		return context.DeadlineExceeded
	}
	if err := r.Store.RenewOutboxClaim(ctx, id, token, lease); err != nil {
		return err
	}
	r.renewals.Add(1)
	select {
	case r.renewed <- struct{}{}:
	default:
	}
	return nil
}

// At-least-once delivery permits serial retries after renewal fails. It must
// not overlap the cancelled delivery or allow an expired claim to continue.
func TestLongRunningDispatcherRetriesAfterLeaseFailureWithoutConcurrentDelivery(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	probe := &leaseTestRepository{Store: store}
	probe.failOnce.Store(true)
	delivery := &leaseTestDeliverer{started: make(chan struct{}), retried: make(chan struct{}), release: make(chan struct{})}
	reported := make(chan error, 8)
	dispatcher, err := NewDispatcher(probe, delivery, Options{WorkerID: "retry-worker", BatchSize: 1,
		Lease: 2 * time.Second, LongRunning: true, RetryBase: 10 * time.Millisecond, RetryMax: 10 * time.Millisecond,
		OnError: func(err error) { reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("dispatcher did not stop")
		}
	}()
	select {
	case <-delivery.retried:
	case <-time.After(4 * time.Second):
		t.Fatal("renewal failure did not cancel and safely retry")
	}
	select {
	case err := <-reported:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unexpected retry cause: %v", err)
		}
	default:
		t.Fatal("renewal failure was not reported")
	}
	if delivery.cancelled.Load() != 1 || delivery.peakActive.Load() != 1 {
		t.Fatalf("retry overlapped cleanup: cancelled=%d peak=%d", delivery.cancelled.Load(), delivery.peakActive.Load())
	}
	close(delivery.release)
	waitForOutboxStatus(t, store, event.ID, workspace.OutboxStatusDelivered, 2*time.Second)
	if delivery.calls.Load() != 2 || delivery.peakActive.Load() != 1 {
		t.Fatalf("unsafe retry: calls=%d peak=%d", delivery.calls.Load(), delivery.peakActive.Load())
	}
}
