package routinescheduler

import (
	"context"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

type stubRoutineRepository struct{}

func (stubRoutineRepository) ClaimNextDueRoutine(time.Time, time.Duration) (workspace.Routine, bool, error) {
	return workspace.Routine{}, false, nil
}

func (stubRoutineRepository) CompleteRoutineTick(string, time.Time, bool, string) error { return nil }

func (stubRoutineRepository) NextRoutineWakeAt(time.Duration) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func TestNewRejectsUnsafeSchedulerOptions(t *testing.T) {
	invalid := map[string]Options{
		"nil repository": {Executor: executorFunc(nil)},
		"nil executor":   {Repository: stubRoutineRepository{}},
		"unbounded ticks without claim renewal": {
			Repository: stubRoutineRepository{}, Executor: executorFunc(nil),
			LockTTL: time.Second, UnboundedTicks: true,
		},
		"tick timeout at lock ttl": {
			Repository: stubRoutineRepository{}, Executor: executorFunc(nil),
			LockTTL: time.Second, TickTimeout: time.Second,
		},
	}
	for name, options := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := New(options); err == nil {
				t.Fatal("unsafe scheduler options were accepted")
			}
		})
	}
}

func TestSchedulerStartAndStopRequireContexts(t *testing.T) {
	scheduler, err := New(Options{
		Repository: stubRoutineRepository{},
		Executor:   executorFunc(func(context.Context, Tick) (Result, error) { return Result{}, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(nil); err == nil {
		t.Fatal("nil start context was accepted")
	}
	if err := scheduler.Stop(nil); err == nil {
		t.Fatal("nil stop context was accepted")
	}
}
