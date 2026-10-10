package routinescheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWakeStartsNewlyCreatedDueRoutine(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workspace.db")
	store := openStore(t, databasePath)
	defer store.Close()

	executed := make(chan Tick, 1)
	scheduler := newTestScheduler(t, store, executorFunc(func(_ context.Context, tick Tick) (Result, error) {
		executed <- tick
		return Result{Summary: "woken"}, nil
	}), nil)
	startScheduler(t, scheduler)
	defer stopScheduler(t, scheduler)

	// Give the loop time to observe an empty repository and block on waitForWake
	// instead of periodically scanning SQLite.
	time.Sleep(50 * time.Millisecond)
	now := time.Now().UTC()
	createDueRoutine(t, store, "wake", now)
	scheduler.Wake()
	tick := receive(t, executed)
	if tick.RoutineID != "routine-wake" || tick.Attempt != 1 {
		t.Fatalf("woken tick = %#v", tick)
	}
	waitFor(t, time.Second, func() bool {
		routine, err := store.GetRoutine("routine-wake")
		return err == nil && routine.TickCount == 1 && routine.LastResults == "woken"
	})
}

func TestWakeIsNonBlockingWhenAlreadySignaled(t *testing.T) {
	scheduler := &Scheduler{wake: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() {
		scheduler.Wake()
		scheduler.Wake()
		scheduler.Wake()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wake blocked after the coalesced signal was already queued")
	}
}
