package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrimaryRuntimeShutdownCancelsAndJoinsBeforeClose(t *testing.T) {
	runContext, cancelCause := context.WithCancelCause(t.Context())
	cause := errors.New("controlled infrastructure shutdown")
	workers := &runtimePrimaryWorkers{}
	var completed atomic.Int32
	for range 3 {
		workers.Go(func() {
			<-runContext.Done()
			if context.Cause(runContext) == cause {
				completed.Add(1)
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	closed := false
	err := shutdownPrimaryRuntime(ctx, func() { cancelCause(cause) }, workers, time.Second, func(context.Context) error {
		closed = true
		if completed.Load() != 3 {
			return fmt.Errorf("runtime closed before owned workers joined: %d", completed.Load())
		}
		return nil
	})
	if err != nil || !closed {
		t.Fatalf("shutdown err=%v close called=%t", err, closed)
	}
	if err := workers.Wait(ctx); err != nil {
		t.Fatalf("idempotent wait: %v", err)
	}
}

func TestPrimaryRuntimeShutdownJoinDeadlineStillClosesAndRetainsErrors(t *testing.T) {
	workers := &runtimePrimaryWorkers{}
	release := make(chan struct{})
	workers.Go(func() { <-release })
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := workers.Wait(ctx); err != nil {
			t.Errorf("release controlled worker: %v", err)
		}
	}()
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	closeFailure := errors.New("controlled close failure")
	cancelled, closed := false, false
	err := shutdownPrimaryRuntime(ctx, func() { cancelled = true }, workers, time.Second, func(received context.Context) error {
		closed = true
		if received.Err() != nil {
			t.Fatalf("exhausted primary join prevented runtime cleanup: %v", received.Err())
		}
		deadline, bounded := received.Deadline()
		if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
			t.Fatal("runtime close did not retain its separate bounded budget")
		}
		return closeFailure
	})
	if !cancelled || !closed || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, closeFailure) {
		t.Fatalf("deadline lost cleanup or causes: cancel=%t close=%t err=%v", cancelled, closed, err)
	}
}

func TestPrimaryRuntimeCloseBudgetRemainsBoundedAfterJoinTimeout(t *testing.T) {
	workers := &runtimePrimaryWorkers{}
	release := make(chan struct{})
	workers.Go(func() { <-release })
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := workers.Wait(ctx); err != nil {
			t.Errorf("release controlled worker: %v", err)
		}
	}()
	type cleanupValueKey struct{}
	parent := context.WithValue(t.Context(), cleanupValueKey{}, "retained diagnostic")
	joinContext, joinCancel := context.WithCancel(parent)
	joinCancel()
	err := shutdownPrimaryRuntime(joinContext, func() {}, workers, 5*time.Millisecond, func(ctx context.Context) error {
		if ctx.Err() != nil || ctx.Value(cleanupValueKey{}) != "retained diagnostic" {
			t.Fatal("fresh cleanup context lost liveness or diagnostic values")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("join cancellation or cleanup deadline was lost: %v", err)
	}
}

func TestRuntimeCleanupFailureNeverLaunchesReplacement(t *testing.T) {
	for _, stage := range []string{"worker join", "runtime close", "routine stop"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			requested := &runtimeRestartRequestedError{Reason: "controlled restart"}
			runErr := withRuntimeShutdownFailure(requested, failure)
			launched := false
			err := finishRuntimeRun(runErr, func() error { launched = true; return nil })
			var incomplete *runtimeShutdownIncompleteError
			var restart *runtimeRestartRequestedError
			if launched || !errors.Is(err, failure) || !errors.As(err, &incomplete) || !errors.As(err, &restart) {
				t.Fatalf("cleanup failure became successful replacement: launched=%t err=%v", launched, err)
			}
		})
	}
	marker := &runtimeRestartRequestedError{Reason: "not a sole result"}
	for _, runErr := range []error{errors.Join(marker, errors.New("unclassified failure")), fmt.Errorf("unclassified wrapper: %w", marker)} {
		launched := false
		if err := finishRuntimeRun(runErr, func() error { launched = true; return nil }); err != runErr || launched {
			t.Fatalf("a non-sole restart marker launched replacement: launched=%t err=%v", launched, err)
		}
	}
}

func TestRuntimeReplacementRequiresCleanShutdown(t *testing.T) {
	requested := &runtimeRestartRequestedError{Reason: "controlled restart"}
	launched := 0
	err := finishRuntimeRun(withRuntimeShutdownFailure(requested, nil), func() error { launched++; return nil })
	if err != nil || launched != 1 {
		t.Fatalf("clean restart: launched=%d err=%v", launched, err)
	}
	for _, runErr := range []error{nil, errors.New("ordinary startup failure")} {
		if err := finishRuntimeRun(runErr, func() error { launched++; return nil }); err != runErr || launched != 1 {
			t.Fatalf("non-restart result launched replacement: launched=%d err=%v", launched, err)
		}
	}
	launchFailure := errors.New("controlled launch failure")
	if err := finishRuntimeRun(requested, func() error { return launchFailure }); !errors.Is(err, launchFailure) {
		t.Fatalf("replacement launch error was lost: %v", err)
	}
}
