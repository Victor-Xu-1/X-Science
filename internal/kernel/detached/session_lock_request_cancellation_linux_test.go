//go:build linux

package detached

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnsureSessionQueuedLockHonorsOriginalRequestCancellation(t *testing.T) {
	b, spec, backend, launcher := sessionResourceWaitFixture(t)
	first, cancelFirst := context.WithCancel(context.Background())
	task, cancelTask := context.WithCancel(context.Background())
	request, cancelRequest := context.WithCancel(task)
	// The production server exposes this same ResourceAdmissionWaitContextSource
	// contract: setup remains live while the original request/lease is obsolete.
	setup := resourceWaitRequestContext{Context: task, request: request}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	firstFinished, secondFinished := false, false
	defer func() {
		cancelRequest()
		cancelTask()
		cancelFirst()
		for _, pending := range []struct {
			finished bool
			done     <-chan error
		}{{firstFinished, firstDone}, {secondFinished, secondDone}} {
			if !pending.finished {
				select {
				case <-pending.done:
				case <-time.After(time.Second):
					t.Error("fixture cleanup did not join EnsureSession")
				}
			}
		}
	}()
	go func() { _, err := b.EnsureSession(first, spec); firstDone <- err }()
	select {
	case <-launcher.observed:
	case <-time.After(time.Second):
		t.Fatal("first invocation did not reach real resource admission")
	}
	go func() { _, err := b.EnsureSession(setup, spec); secondDone <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		b.sessionLocksMu.Lock()
		lock := b.sessionLocks[spec.KernelID]
		queued := lock != nil && lock.references == 2
		b.sessionLocksMu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second invocation did not queue behind the same kernel lock")
		}
		time.Sleep(time.Millisecond)
	}
	cancelRequest()
	if task.Err() != nil || setup.Err() != nil || request.Err() == nil {
		t.Fatal("fixture did not preserve a live task/setup behind a cancelled request")
	}
	select {
	case err := <-secondDone:
		secondFinished = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("obsolete second waiter returned the wrong cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("obsolete original request remains blocked in the same-kernel session lock while task/setup stay live")
	}
	select {
	case err := <-firstDone:
		firstFinished = true
		t.Errorf("second waiter cancellation disturbed the original capacity wait: %v", err)
	default:
	}
	reservation, found, err := b.Store.KernelResourceReservation(context.Background(), backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || reservation.State != "waiting" || reservation.ReservedBytes != 0 || launcher.launches.Load() != 0 {
		t.Errorf("lock cancellation mutated durable admission or launched: reservation=%+v found=%t launches=%d err=%v", reservation, found, launcher.launches.Load(), err)
	}
}
