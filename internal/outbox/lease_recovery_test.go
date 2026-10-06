package outbox

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

func TestLongRunningDispatcherPreservesDeliveryAcrossTransientRenewalFailure(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	probe := &leaseTestRepository{Store: store, renewed: make(chan struct{}, 8)}
	probe.failOnce.Store(true)
	delivery := &leaseTestDeliverer{started: make(chan struct{}), release: make(chan struct{})}
	dispatcher, err := NewDispatcher(probe, delivery, Options{
		WorkerID: "recovery-worker", BatchSize: 1, Lease: 2 * time.Second,
		LongRunning: true, RetryBase: 10 * time.Millisecond, RetryMax: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("dispatcher did not stop")
		}
	})
	select {
	case <-probe.renewed:
	case <-time.After(4 * time.Second):
		t.Fatal("transient renewal did not recover inside the valid claim")
	}
	if delivery.calls.Load() != 1 || delivery.cancelled.Load() != 0 {
		t.Fatalf("valid delivery was restarted: calls=%d cancelled=%d", delivery.calls.Load(), delivery.cancelled.Load())
	}
	close(delivery.release)
	waitForOutboxStatus(t, store, event.ID, workspace.OutboxStatusDelivered, 2*time.Second)
}

func TestLongRunningDispatcherCannotStartWithExpiredClaim(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	claimed, err := store.ClaimOutbox(t.Context(), workspace.ClaimOutboxInput{
		WorkerID: "expired-worker", Topics: []string{event.Topic}, Limit: 1, Lease: time.Second,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	delivery := &leaseTestDeliverer{started: make(chan struct{}), release: make(chan struct{})}
	dispatcher, err := NewDispatcher(store, delivery, Options{WorkerID: "expired-worker", LongRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	claimed[0].LeaseExpiresAt = &expired
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	if err := dispatcher.deliverWithLease(ctx, claimed[0]); err == nil {
		t.Fatal("expired claim was accepted")
	}
	if delivery.calls.Load() != 0 {
		t.Fatal("delivery started under expired claim")
	}
}
