package outbox

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

type leaseTestDeliverer struct {
	calls      atomic.Int32
	started    chan struct{}
	release    chan struct{}
	active     atomic.Int32
	peakActive atomic.Int32
	cancelled  atomic.Int32
	initial    workspace.OutboxEvent
	retried    chan struct{}
}

func (d *leaseTestDeliverer) Deliver(ctx context.Context, event workspace.OutboxEvent) error {
	if _, ok := ctx.Deadline(); ok {
		return context.DeadlineExceeded
	}
	active := d.active.Add(1)
	defer d.active.Add(-1)
	for peak := d.peakActive.Load(); active > peak; peak = d.peakActive.Load() {
		if d.peakActive.CompareAndSwap(peak, active) {
			break
		}
	}
	if calls := d.calls.Add(1); calls == 1 {
		d.initial = event
		close(d.started)
	} else if calls == 2 && d.retried != nil {
		close(d.retried)
	}
	select {
	case <-ctx.Done():
		d.cancelled.Add(1)
		return ctx.Err()
	case <-d.release:
		return nil
	}
}

func TestLongRunningDispatcherRenewsLeaseWithoutConcurrentDelivery(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	delivery := &leaseTestDeliverer{started: make(chan struct{}), release: make(chan struct{})}
	probe := &leaseTestRepository{Store: store, renewed: make(chan struct{}, 8)}
	reported := make(chan error, 8)
	// This is the healthy-renewal integration path, not a scheduler stress test.
	// Keep the operation alive beyond its original DB lease and observe real
	// successful renewals instead of assuming a 60ms renewal deadline survived
	// shared CI scheduling. The separate fault test still requires cancellation.
	dispatcher, err := NewDispatcher(probe, delivery, Options{WorkerID: "long-worker", BatchSize: 1, Lease: 2 * time.Second, LongRunning: true, PollInterval: 10 * time.Millisecond,
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
	case <-delivery.started:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not start")
	}
	if delivery.initial.LeaseExpiresAt == nil {
		t.Fatal("initial delivery has no durable lease")
	}
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	for !time.Now().After(*delivery.initial.LeaseExpiresAt) || probe.renewals.Load() < 3 {
		select {
		case <-probe.renewed:
		case err := <-reported:
			t.Fatalf("healthy lease renewal failed: %v", err)
		case <-deadline.C:
			t.Fatal("did not observe healthy renewal beyond the original lease")
		}
	}
	claimed, err := store.ClaimOutbox(ctx, workspace.ClaimOutboxInput{WorkerID: "competitor", Topics: []string{event.Topic}, Limit: 1, Lease: time.Second})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("live operation stolen: %#v %v", claimed, err)
	}
	close(delivery.release)
	settleDeadline := time.Now().Add(2 * time.Second)
	for {
		current, err := store.GetOutboxEvent(ctx, event.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == workspace.OutboxStatusDelivered {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatal("delivery did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if delivery.calls.Load() != 1 {
		t.Fatalf("duplicate executions=%d", delivery.calls.Load())
	}
	if delivery.peakActive.Load() != 1 || delivery.cancelled.Load() != 0 || probe.renewals.Load() < 3 {
		t.Fatalf("healthy renewal proof: peak=%d cancelled=%d renewals=%d", delivery.peakActive.Load(), delivery.cancelled.Load(), probe.renewals.Load())
	}
}

func TestLongRunningDispatcherCancelsExecutionOnClaimLoss(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	delivery := &leaseTestDeliverer{started: make(chan struct{}), release: make(chan struct{})}
	dispatcher, err := NewDispatcher(store, delivery, Options{WorkerID: "long-worker", BatchSize: 1, Lease: 90 * time.Millisecond, LongRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(context.Background(), workspace.ClaimOutboxInput{WorkerID: "owner", Topics: []string{event.Topic}, Limit: 1, Lease: time.Second})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%#v %v", claimed, err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- dispatcher.deliverWithLease(ctx, claimed[0]) }()
	select {
	case <-delivery.started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	if err := store.AckOutbox(ctx, event.ID, claimed[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("claim loss did not cancel execution")
		}
	case <-time.After(time.Second):
		t.Fatal("execution continued after losing claim")
	}
}
