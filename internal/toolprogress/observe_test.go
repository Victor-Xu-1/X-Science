package toolprogress

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestObserveReportsExecutionExitWithoutResult(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := Observe(t.Context(), time.Millisecond, func(context.Context) (string, error) {
			runtime.Goexit()
			return "", nil
		}, func(*Update, time.Duration, int) {})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("execution without a result was reported as success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observer remained running after its execution goroutine exited")
	}
}

func TestObserveReturnsSuccessfulResult(t *testing.T) {
	result, err := Observe(context.Background(), time.Millisecond, func(context.Context) (string, error) {
		return "installed", nil
	}, func(*Update, time.Duration, int) {})
	if err != nil || result != "installed" {
		t.Fatalf("result=%q error=%v", result, err)
	}
}

func TestObserveSurfacesExecutionPanicInsteadOfHanging(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := Observe(context.Background(), time.Millisecond, func(context.Context) (string, error) {
			panic("installer exploded")
		}, func(*Update, time.Duration, int) {})
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe hung after the tool goroutine panicked")
	}
	if err == nil || !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), "installer exploded") {
		t.Fatalf("error=%v", err)
	}
}

func TestObserveStillReturnsOrdinaryExecutionErrors(t *testing.T) {
	want := errors.New("conda failed")
	_, err := Observe(context.Background(), time.Millisecond, func(context.Context) (string, error) {
		return "", want
	}, func(*Update, time.Duration, int) {})
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}
