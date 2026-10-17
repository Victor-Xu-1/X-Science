package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagedCleanupObservationRetainsPhysicalOwner(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		name := "cleanup_unknown"
		if cancelFirst {
			name = "last_waiter_cancelled"
		}
		t.Run(name, func(t *testing.T) { verifyManagedCleanupReservation(t, cancelFirst) })
	}
}

func TestManagedCleanupObservationSurvivesConcurrentCancellationAndSettlement(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(Config{Micromamba: filepath.Join(root, "installer"),
		CondaHome: filepath.Join(root, "conda"), CondaEnvsPath: filepath.Join(root, "conda", "envs")})
	startManagedEnvironmentSupervisor(t, manager)
	var calls atomic.Int32
	for iteration := 0; iteration < 32; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		result, err := manager.runManagedEnvironmentOperation(ctx, "settlement-race", func(work context.Context) (ManagedEnvironment, error) {
			calls.Add(1)
			reportWorkerCleanup(work, &WorkerProcessCleanupError{State: "cleanup_unknown"})
			cancel()
			// All observation, cancellation and done channels can now be ready.
			// A late return cannot convert the already observed uncertainty to success.
			return ManagedEnvironment{Name: "must-not-publish"}, nil
		})
		cancel()
		assertManagedCleanupError(t, err)
		if result.Name != "" {
			t.Fatalf("late settlement returned a successful environment: %+v", result)
		}
		deadline := time.Now().Add(time.Second)
		for manager.ActiveManagedEnvironmentOperationCount() != 0 {
			if time.Now().After(deadline) {
				t.Fatal("settled fixture owner remained reserved")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if count := calls.Load(); count != 32 {
		t.Fatalf("race controls did not execute their actual operation: %d", count)
	}
}

func verifyManagedCleanupReservation(t *testing.T, cancelFirst bool) {
	t.Helper()
	root := t.TempDir()
	manager := NewManager(Config{Micromamba: filepath.Join(root, "installer"),
		CondaHome: filepath.Join(root, "conda"), CondaEnvsPath: filepath.Join(root, "conda", "envs")})
	startManagedEnvironmentSupervisor(t, manager)
	key, lockPath, staging := "cleanup-reservation", filepath.Join(root, "operation.lock"), filepath.Join(root, "staging")
	started, trigger, physicalSettlement, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	waiterDone := make(chan error, 1)
	waitCtx, cancelWaiter := context.WithCancel(context.Background())
	var releaseOnce sync.Once
	var cleanupCount, publicationCount atomic.Int32
	go func() {
		_, err := manager.runManagedEnvironmentOperation(waitCtx, key, func(ctx context.Context) (ManagedEnvironment, error) {
			defer close(finished)
			release, err := lockKernelFile(ctx, lockPath)
			if err != nil {
				return ManagedEnvironment{}, err
			}
			defer release()
			if err := os.Mkdir(staging, 0o700); err != nil {
				return ManagedEnvironment{}, err
			}
			defer func() { _ = os.RemoveAll(staging); cleanupCount.Add(1) }()
			close(started)
			select {
			case <-trigger:
			case <-ctx.Done():
			}
			failure := &WorkerProcessCleanupError{State: "cleanup_unknown", Cause: errors.New("terminal receipt lost")}
			if !reportWorkerCleanup(ctx, failure) || !reportWorkerCleanup(ctx, failure) {
				return ManagedEnvironment{}, errors.New("operation cleanup observer was not bound")
			}
			<-physicalSettlement
			if err := ctx.Err(); err != nil {
				return ManagedEnvironment{}, err
			}
			publicationCount.Add(1)
			return ManagedEnvironment{Name: "must-not-publish"}, nil
		})
		waiterDone <- err
	}()
	t.Cleanup(func() {
		cancelWaiter()
		releaseOnce.Do(func() { close(physicalSettlement) })
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("fixture physical owner did not settle")
		}
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fixture operation did not start")
	}
	manager.managedEnvironmentMu.Lock()
	owner := manager.managedEnvironmentOperations[key]
	manager.managedEnvironmentMu.Unlock()
	if cancelFirst {
		cancelWaiter()
	} else {
		close(trigger)
	}
	select {
	case err := <-waiterDone:
		assertManagedCleanupError(t, err)
	case <-time.After(time.Second):
		t.Fatal("observer remained blocked on physical cleanup")
	}
	assertManagedCleanupReservationHeld(t, manager, key, owner, lockPath, staging)
	var replayCount atomic.Int32
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), time.Second)
	defer cancelProbe()
	for _, requestKey := range []string{key, key + "-new-request"} {
		_, err := manager.runManagedEnvironmentOperation(probeCtx, requestKey, func(context.Context) (ManagedEnvironment, error) {
			replayCount.Add(1)
			return ManagedEnvironment{}, nil
		})
		assertManagedCleanupError(t, err)
	}
	if probeCtx.Err() != nil || replayCount.Load() != 0 {
		t.Fatal("conflicting waiter timed out or restarted an uncertain operation")
	}
	assertManagedCleanupReservationHeld(t, manager, key, owner, lockPath, staging)
	releaseOnce.Do(func() { close(physicalSettlement) })
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("late physical settlement did not release the original owner")
	}
	if cleanupCount.Load() != 1 || publicationCount.Load() != 0 {
		t.Fatalf("cleanup=%d publication=%d", cleanupCount.Load(), publicationCount.Load())
	}
	if count := manager.ActiveManagedEnvironmentOperationCount(); count != 0 {
		t.Fatalf("settled physical operations still active: %d", count)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled staging was retained: %v", err)
	}
	lockCtx, cancelLock := context.WithTimeout(context.Background(), time.Second)
	defer cancelLock()
	release, err := lockKernelFile(lockCtx, lockPath)
	if err != nil {
		t.Fatalf("settled operation lock was not released: %v", err)
	}
	release()
}

func assertManagedCleanupError(t *testing.T, err error) {
	t.Helper()
	var cleanup *WorkerProcessCleanupError
	if !errors.As(err, &cleanup) || cleanup.State != "cleanup_unknown" {
		t.Fatalf("expected observable cleanup_unknown, got %v", err)
	}
}

func assertManagedCleanupReservationHeld(t *testing.T, manager *Manager, key string,
	owner *managedEnvironmentOperation, lockPath, staging string) {
	t.Helper()
	manager.managedEnvironmentMu.Lock()
	current := manager.managedEnvironmentOperations[key]
	manager.managedEnvironmentMu.Unlock()
	if owner == nil || current != owner {
		t.Fatal("uncertain operation lost its physical reservation")
	}
	if count := manager.ActiveManagedEnvironmentOperationCount(); count != 1 {
		t.Fatalf("unsettled physical owner missing from active work: %d", count)
	}
	select {
	case <-owner.done:
		t.Fatal("unknown observation was presented as physical settlement")
	default:
	}
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("unsettled staging was deleted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := lockKernelFile(ctx, lockPath); err == nil {
		release()
		t.Fatal("unsettled physical lock was released")
	}
}
