//go:build linux

package detached

import (
	"context"
	"errors"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

type resourceNotificationLauncher struct {
	launch func(ExecutorLaunchRequest) error
}

func (l resourceNotificationLauncher) Launch(request ExecutorLaunchRequest) error {
	return l.launch(request)
}
func (resourceNotificationLauncher) Observe(context.Context, string, int64) (ExecutorLaunchState, error) {
	return ExecutorLaunchExited, nil
}
func (resourceNotificationLauncher) ResourceCapacity(context.Context, int64) (workspace.KernelResourceCapacity, executorCapacityObservation, error) {
	return workspace.KernelResourceCapacity{AvailableBytes: 2 << 30, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{}}, executorCapacityObservation{AvailableBytes: 2 << 30}, nil
}
func (resourceNotificationLauncher) ResourceUsage(context.Context, string, int64) (int64, ExecutorLaunchState, error) {
	return 0, ExecutorLaunchUnknown, nil
}

func deferredResourceNotificationBackend(t *testing.T) (*workspace.Store, workspace.KernelExecutionBackend) {
	t.Helper()
	store, backend := startupBackendFixture(t)
	if _, ok, err := store.AdmitKernelResources(context.Background(), backend, 0, workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: executorMemoryFloorBytes}); err != nil || ok {
		t.Fatal("fixture did not persist a resource wait", ok, err)
	}
	return store, backend
}

func TestDeferredResourceNotificationIsVisibleBeforeControlledLaunch(t *testing.T) {
	store, backend := deferredResourceNotificationBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	launcher := resourceNotificationLauncher{launch: func(ExecutorLaunchRequest) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	b := &Backend{Store: store, Launcher: launcher, Executable: "/bin/true", HomeDir: t.TempDir(), LogDir: t.TempDir()}
	done := make(chan error, 1)
	go func() {
		handled, err := b.reconcileResourceWait(ctx, backend)
		if !handled && err == nil {
			err = errors.New("existing resource supervisor did not handle waiting generation")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("controlled launcher did not enter", ctx.Err())
	}
	access, found, err := store.GetKernelFrameAccessContext(ctx, backend.FrameID)
	if err != nil || !found {
		t.Errorf("access found=%t err=%v", found, err)
	} else {
		if count, err := store.CountPendingKernelResourceWaits(ctx, access); err != nil || count != 1 {
			t.Errorf("reserved startup became falsely idle: count=%d err=%v", count, err)
		}
		notices, err := store.ClaimUnreadNotifications(ctx, backend.FrameID, backend.RootFrameID, backend.OwnerUserID, "controlled-launch-handoff", 10)
		if err != nil || len(notices) != 1 || notices[0].NotificationType != "resource_capacity_admitted" || notices[0].Payload["executed"] != false {
			t.Errorf("admission notice not committed before launch: count=%d err=%v", len(notices), err)
		}
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("controlled launcher did not finish", ctx.Err())
	}
}

func TestDeferredResourceNotificationDoesNotInventExecutionOnLaunchFailure(t *testing.T) {
	store, backend := deferredResourceNotificationBackend(t)
	ctx := context.Background()
	launchErr := errors.New("controlled launch failure without a process")
	b := &Backend{Store: store, Executable: "/bin/true", HomeDir: t.TempDir(), LogDir: t.TempDir(),
		Launcher: resourceNotificationLauncher{launch: func(ExecutorLaunchRequest) error { return launchErr }}}
	if handled, err := b.reconcileResourceWait(ctx, backend); !handled || !errors.Is(err, launchErr) {
		t.Fatalf("launch failure lost: handled=%t err=%v", handled, err)
	}
	receipt, found, err := store.GetKernelStartupFailure(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || receipt.Stage != "launch" {
		t.Fatalf("real launch failure receipt missing: %#v found=%t err=%v", receipt, found, err)
	}
	reservation, found, err := store.KernelResourceReservation(ctx, backend.BackendID, backend.BackendGeneration)
	if err != nil || !found || reservation.State != "released" {
		t.Fatalf("positive exited observation did not settle resources: %#v %v", reservation, err)
	}
	notices, err := store.ClaimUnreadNotifications(ctx, backend.FrameID, backend.RootFrameID, backend.OwnerUserID, "controlled-launch-failure", 10)
	if err != nil || len(notices) != 1 || notices[0].Payload["executed"] != false || notices[0].Payload["status"] != "execution_capacity_admitted" {
		t.Errorf("historical capacity notice claimed execution or was lost: count=%d err=%v", len(notices), err)
	}
}
