//go:build linux

package detached

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitForSessionLockReferences(t *testing.T, backend *Backend, key string, references int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		backend.sessionLocksMu.Lock()
		lock := backend.sessionLocks[key]
		matched := lock != nil && lock.references == references
		backend.sessionLocksMu.Unlock()
		if matched {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session lock did not reach %d references", references)
		}
		time.Sleep(time.Millisecond)
	}
}

func assertSessionLockReleased(t *testing.T, backend *Backend) {
	t.Helper()
	backend.sessionLocksMu.Lock()
	defer backend.sessionLocksMu.Unlock()
	if len(backend.sessionLocks) != 0 {
		t.Fatal("cancelled acquisition leaked a session gate reference")
	}
}

func TestBackendSessionLockPreservesOriginalCancellationCause(t *testing.T) {
	backend := &Backend{}
	first, err := backend.lockSession(context.Background(), "same-key")
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(first) }
	defer release()
	task, cancelTask := context.WithCancel(context.Background())
	defer cancelTask()
	request, cancelRequest := context.WithCancelCause(task)
	defer cancelRequest(nil)
	setup := resourceWaitRequestContext{Context: task, request: request}
	done := make(chan error, 1)
	go func() {
		unlock, err := backend.lockSession(setup, "same-key")
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	waitForSessionLockReferences(t, backend, "same-key", 2)
	cause := errors.New("original runner lease was revoked")
	cancelRequest(cause)
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Fatalf("lock wait lost the original cancellation cause: %v", err)
		}
	case <-time.After(time.Second):
		cancelTask()
		release()
		<-done
		t.Fatal("original request did not stop the lock waiter")
	}
	if setup.Err() != nil || task.Err() != nil {
		t.Fatal("lock waiter cancellation escaped into the setup lifetime")
	}
	waitForSessionLockReferences(t, backend, "same-key", 1)
	release()
	assertSessionLockReleased(t, backend)
}

func TestBackendSessionLockRejectsCancelledRequestWithAvailableGate(t *testing.T) {
	backend := &Backend{}
	cause := errors.New("original request is obsolete before gate acquisition")
	request, cancelRequest := context.WithCancelCause(context.Background())
	cancelRequest(cause)
	setup := resourceWaitRequestContext{Context: context.Background(), request: request}
	// Cancellation and the initially available gate are both selectable. Every
	// acquisition outcome must return the gate/reference rather than transfer it.
	for attempt := 0; attempt < 64; attempt++ {
		unlock, err := backend.lockSession(setup, "same-key")
		if unlock != nil {
			unlock()
			t.Fatal("cancelled request acquired the available session gate")
		}
		if !errors.Is(err, cause) {
			t.Fatalf("cancelled acquisition cause=%v", err)
		}
		assertSessionLockReleased(t, backend)
	}
}

func TestBackendSessionLockKeepsAcquiredSetupAliveAcrossRequestHandoff(t *testing.T) {
	backend := &Backend{}
	task, cancelTask := context.WithCancel(context.Background())
	defer cancelTask()
	request, cancelRequest := context.WithCancel(task)
	defer cancelRequest()
	setup := resourceWaitRequestContext{Context: task, request: request}
	unlock, err := backend.lockSession(setup, "same-key")
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(unlock) }
	defer release()
	cancelRequest()
	if setup.Err() != nil || task.Err() != nil {
		t.Fatal("request handoff cancelled an already admitted setup lifetime")
	}
	competing, cancelCompeting := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelCompeting()
	other, err := backend.lockSession(competing, "same-key")
	if other != nil {
		other()
		t.Fatal("request handoff released the still-owned session gate")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("competing waiter did not retain its own deadline: %v", err)
	}
	release()
	assertSessionLockReleased(t, backend)
	final, err := backend.lockSession(context.Background(), "same-key")
	if err != nil {
		t.Fatal(err)
	}
	final()
	assertSessionLockReleased(t, backend)
}
