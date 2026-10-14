package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeResourcesRemainOpenUnderUnjoinedCallback(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "owned-writer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	resources := &runtimePrimaryResources{}
	var closed atomic.Bool
	resources.Add("writer target", func() error { closed.Store(true); return file.Close() })
	release, done := make(chan struct{}), make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		<-done
	})
	wrote := make(chan error, 1)
	go func() {
		defer close(done)
		<-release
		_, err := file.WriteString("callback settlement\n")
		wrote <- err
	}()
	owners := &runtimePrimaryClosers{}
	owners.Add("blocked callback", func(ctx context.Context) error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	join, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	err = shutdownPrimaryRuntime(join, cancel, &runtimePrimaryWorkers{}, time.Millisecond, owners.Close)
	runErr := resources.CloseAfter(withRuntimeShutdownFailure(&runtimeRestartRequestedError{Reason: "reload"}, err))
	if !errors.Is(runErr, context.DeadlineExceeded) || closed.Load() || ctx.Err() == nil {
		t.Fatalf("incomplete cleanup must cancel owners and retain stores: err=%v closed=%v", runErr, closed.Load())
	}
	replaced := false
	if finishRuntimeRun(runErr, func() error { replaced = true; return nil }) == nil || replaced {
		t.Fatal("unjoined writer authorized a replacement")
	}
	close(release)
	released = true
	<-done
	if err := <-wrote; err != nil {
		t.Fatalf("callback lost its live persistence target: %v", err)
	}
}

func TestRuntimeCleanShutdownClosesOwnersThenPersistentResources(t *testing.T) {
	var order []string
	owners := &runtimePrimaryClosers{}
	for _, name := range []string{"IM", "plugins", "server"} {
		owners.Add(name, func(context.Context) error { order = append(order, name); return nil })
	}
	resources := &runtimePrimaryResources{}
	for _, name := range []string{"log", "state", "workspace"} {
		resources.Add(name, func() error { order = append(order, name); return nil })
	}
	join, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	err := shutdownPrimaryRuntime(join, func() { order = append(order, "cancel") }, &runtimePrimaryWorkers{}, time.Second, owners.Close)
	err = resources.CloseAfter(withRuntimeShutdownFailure(&runtimeRestartRequestedError{Reason: "reload"}, err))
	err = finishRuntimeRun(err, func() error { order = append(order, "replacement"); return nil })
	want := []string{"cancel", "server", "plugins", "IM", "workspace", "state", "log", "replacement"}
	if err != nil || !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v err=%v", order, want, err)
	}
}

func TestRuntimeResourceCloseFailuresDoNotSkipRemainingResources(t *testing.T) {
	first, second := errors.New("workspace flush"), errors.New("log flush")
	resources := &runtimePrimaryResources{}
	var order []string
	resources.Add("log", func() error { order = append(order, "log"); return second })
	resources.Add("state", func() error { order = append(order, "state"); return nil })
	resources.Add("workspace", func() error { order = append(order, "workspace"); return first })
	err := resources.CloseAfter(&runtimeRestartRequestedError{Reason: "reload"})
	var incomplete *runtimeShutdownIncompleteError
	if !errors.As(err, &incomplete) || !errors.Is(err, first) || !errors.Is(err, second) || !reflect.DeepEqual(order, []string{"workspace", "state", "log"}) {
		t.Fatalf("resource flush errors/order lost: err=%v order=%v", err, order)
	}
	replaced := false
	_ = finishRuntimeRun(err, func() error { replaced = true; return nil })
	if replaced {
		t.Fatal("failed resource close authorized replacement")
	}
}

func TestRuntimePartialStartupClosesOnlyRegisteredResources(t *testing.T) {
	startupErr := errors.New("startup refused")
	resources := &runtimePrimaryResources{}
	closed := 0
	resources.Add("state", func() error { closed++; return nil })
	if err := resources.CloseAfter(startupErr); err != startupErr || closed != 1 {
		t.Fatalf("partial startup lost original error/cleanup: err=%v closed=%d", err, closed)
	}
}

func TestRuntimeOwnerFailureStillCancelsRemainingOwners(t *testing.T) {
	owners := &runtimePrimaryClosers{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var order []string
	owners.Add("IM", func(actual context.Context) error {
		order = append(order, "IM")
		if actual != ctx {
			t.Fatal("remaining owner did not share the cleanup budget")
		}
		return nil
	})
	owners.Add("server", func(context.Context) error { order = append(order, "server"); return context.DeadlineExceeded })
	if err := owners.Close(ctx); !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(order, []string{"server", "IM"}) {
		t.Fatalf("owner error skipped remaining cancellation: err=%v order=%v", err, order)
	}
}
