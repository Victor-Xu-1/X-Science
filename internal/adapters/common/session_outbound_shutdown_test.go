package common

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func closeSessionOutboundTestDispatcher(t *testing.T, dispatcher *SessionOutboundDispatcher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Errorf("close outbound dispatcher: %v", err)
	}
}

func waitSessionOutboundShutdownSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled outbound lifecycle signal did not arrive")
	}
}

func TestSessionOutboundCloseWaitsForUnboundWorkerAndResult(t *testing.T) {
	started, resultStarted, releaseResult := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var callbacks atomic.Int32
	dispatcher, err := NewSessionOutboundDispatcher(t.Context(), SessionOutboundDispatcherOptions{
		Handlers: map[string]SessionOutboundHandler{"wechat": func(ctx context.Context, _ IMSessionBinding, _ ServerMessage) (OutboundReport, error) {
			close(started)
			<-ctx.Done()
			return OutboundReport{}, ctx.Err()
		}},
		Delivery: OutboundDeliveryOptions{MaxAttempts: 1},
		OnResult: func(SessionOutboundResult) {
			callbacks.Add(1)
			close(resultStarted)
			<-releaseResult
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSessionOutboundTestDispatcher(t, dispatcher)
	defer releaseOnce.Do(func() { close(releaseResult) })
	binding := IMSessionBinding{SessionID: "im:wechat:closing", Platform: "wechat", ChatID: "closing", OwnerUserID: "owner"}
	if err := dispatcher.Bind(binding); err != nil {
		t.Fatal(err)
	}
	if !dispatcher.Enqueue(SessionOutboundEvent{SessionID: binding.SessionID, EventID: 1, Message: ServerMessage{"type": "error", "message": "controlled"}}) {
		t.Fatal("enqueue controlled delivery")
	}
	waitSessionOutboundShutdownSignal(t, started)
	dispatcher.Unbind(binding.SessionID)
	waitSessionOutboundShutdownSignal(t, resultStarted)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := dispatcher.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("unbound worker's unfinished result was treated as closed: %v", err)
	}
	if err := dispatcher.Bind(binding); err == nil {
		t.Fatal("Bind admitted work after shutdown began")
	}
	if dispatcher.Enqueue(SessionOutboundEvent{SessionID: binding.SessionID, Message: ServerMessage{"type": "message_complete"}}) {
		t.Fatal("Enqueue admitted work after shutdown began")
	}
	releaseOnce.Do(func() { close(releaseResult) })
	closeSessionOutboundTestDispatcher(t, dispatcher)
	if err := dispatcher.Close(cancelled); err != nil {
		t.Fatalf("already finished shutdown lost to cancelled caller: %v", err)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("callbacks=%d, want one actual cancelled delivery result", callbacks.Load())
	}
}

func TestSessionOutboundCloseTracksEnqueueTimeoutResult(t *testing.T) {
	started, resultStarted, releaseResult := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	dispatcher, err := NewSessionOutboundDispatcher(t.Context(), SessionOutboundDispatcherOptions{
		QueueSize: 1, EnqueueWait: time.Millisecond,
		Handlers: map[string]SessionOutboundHandler{"wechat": func(ctx context.Context, _ IMSessionBinding, _ ServerMessage) (OutboundReport, error) {
			close(started)
			<-ctx.Done()
			return OutboundReport{}, ctx.Err()
		}},
		Delivery: OutboundDeliveryOptions{MaxAttempts: 1},
		OnResult: func(result SessionOutboundResult) {
			if result.EventID == 3 {
				close(resultStarted)
				<-releaseResult
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSessionOutboundTestDispatcher(t, dispatcher)
	defer releaseOnce.Do(func() { close(releaseResult) })
	binding := IMSessionBinding{SessionID: "im:wechat:queue-close", Platform: "wechat", ChatID: "queue-close", OwnerUserID: "owner"}
	if err := dispatcher.Bind(binding); err != nil {
		t.Fatal(err)
	}
	event := SessionOutboundEvent{SessionID: binding.SessionID, EventID: 1, Message: ServerMessage{"type": "error", "message": "controlled"}}
	if !dispatcher.Enqueue(event) {
		t.Fatal("enqueue first delivery")
	}
	waitSessionOutboundShutdownSignal(t, started)
	event.EventID = 2
	if !dispatcher.Enqueue(event) {
		t.Fatal("enqueue second delivery into controlled queue")
	}
	event.EventID = 3
	enqueueDone := make(chan bool, 1)
	go func() { enqueueDone <- dispatcher.Enqueue(event) }()
	waitSessionOutboundShutdownSignal(t, resultStarted)
	dispatcher.Unbind(binding.SessionID)
	// The unbound worker must finish, leaving only the Enqueue caller's
	// synchronous OnResult active. Do not confuse a worker-only join with this.
	deadline := time.Now().Add(2 * time.Second)
	for {
		dispatcher.mu.RLock()
		active := dispatcher.active
		dispatcher.mu.RUnlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not leave exactly the blocked enqueue callback: active=%d", active)
		}
		time.Sleep(time.Millisecond)
	}
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if err := dispatcher.Close(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Enqueue callback escaped close lifetime: %v", err)
	}
	releaseOnce.Do(func() { close(releaseResult) })
	select {
	case accepted := <-enqueueDone:
		if accepted {
			t.Fatal("queue-full delivery was accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Enqueue did not return after its result finished")
	}
	closeSessionOutboundTestDispatcher(t, dispatcher)
}

func TestSessionOutboundCloseSerializesBindAndConcurrentClosers(t *testing.T) {
	dispatcher, err := NewSessionOutboundDispatcher(t.Context(), SessionOutboundDispatcherOptions{
		Handlers: map[string]SessionOutboundHandler{"wechat": func(context.Context, IMSessionBinding, ServerMessage) (OutboundReport, error) {
			return OutboundReport{}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSessionOutboundTestDispatcher(t, dispatcher)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range 16 {
		group.Go(func() {
			<-start
			chat := fmt.Sprint("racing-", index)
			_ = dispatcher.Bind(IMSessionBinding{SessionID: "im:wechat:" + chat, Platform: "wechat", ChatID: chat, OwnerUserID: "owner"})
		})
	}
	for range 4 {
		group.Go(func() {
			<-start
			closeSessionOutboundTestDispatcher(t, dispatcher)
		})
	}
	close(start)
	group.Wait()
	if err := dispatcher.Bind(IMSessionBinding{SessionID: "im:wechat:late", Platform: "wechat", ChatID: "late", OwnerUserID: "owner"}); err == nil {
		t.Fatal("late Bind survived concurrent close")
	}
}

func TestSessionOutboundCloseRequiresContextWithoutCancellingDispatcher(t *testing.T) {
	var empty SessionOutboundDispatcher
	if empty.Enqueue(SessionOutboundEvent{SessionID: "missing", Message: ServerMessage{"type": "error"}}) {
		t.Fatal("zero dispatcher admitted a delivery")
	}
	if err := empty.Close(t.Context()); err != nil {
		t.Fatalf("close zero dispatcher: %v", err)
	}
	var absent *SessionOutboundDispatcher
	if err := absent.Close(t.Context()); err != nil {
		t.Fatalf("close nil dispatcher: %v", err)
	}
	dispatcher, err := NewSessionOutboundDispatcher(t.Context(), SessionOutboundDispatcherOptions{
		Handlers: map[string]SessionOutboundHandler{"wechat": func(context.Context, IMSessionBinding, ServerMessage) (OutboundReport, error) {
			return OutboundReport{}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSessionOutboundTestDispatcher(t, dispatcher)
	if err := dispatcher.Close(nil); err == nil {
		t.Fatal("Close accepted a missing caller context")
	}
	if err := dispatcher.Bind(IMSessionBinding{SessionID: "im:wechat:still-open", Platform: "wechat", ChatID: "still-open", OwnerUserID: "owner"}); err != nil {
		t.Fatalf("invalid Close mutated admission state: %v", err)
	}
}
