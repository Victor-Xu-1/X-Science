package workspace

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func resourceNotificationFixture(t *testing.T) (*Store, KernelExecutionBackend, KernelFrameAccess) {
	t.Helper()
	store, repo, claim := newKernelLocalOperationFixture(t)
	op := createKernelLocalOperationForTest(t, store, repo, claim, "resource-notification-source", "resource-notification-call")
	op.KernelID = "resource-notification-kernel"
	backend, err := store.CreateKernelExecutionBackend(context.Background(), CreateKernelExecutionBackendInput{
		BackendID: "resource-notification-backend", OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID,
		FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID, RootFrameID: op.RootFrameID, RootFrameIncarnationID: op.RootFrameIncarnationID,
		KernelID: op.KernelID, KernelGeneration: 1, BackendGeneration: 1, ExecutorInstanceID: "resource-notification-executor",
		MachineBootID: "resource-notification-boot", SocketPath: "/tmp/x-science-resource-notification.sock", SessionSpec: kernelExecutionSessionSpecForTest(op),
	})
	if err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(context.Background(), op.FrameID)
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	return store, backend, access
}

func TestKernelResourceAdmissionNotificationCommitsWithDeferredPromise(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial immediate", true: "persisted waiting"}[deferred], func(t *testing.T) {
			store, backend, access := resourceNotificationFixture(t)
			ctx := context.Background()
			capacity := KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1000, MinimumBytes: 100}
			if deferred {
				if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || ok {
					t.Fatal("fixture did not wait", ok, err)
				}
			}
			capacity.AvailableBytes = 1000
			wake := store.OutboxWake()
			if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || !ok {
				t.Fatal("admission failed", ok, err)
			}
			want := 0
			if deferred {
				want = 1
			}
			select {
			case <-wake:
				if !deferred {
					t.Fatal("initial admission emitted an unrelated notification wake")
				}
			default:
				if deferred {
					t.Fatal("committed deferred admission did not wake the existing wait channel")
				}
			}
			for range 2 {
				if count, err := store.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != want {
					t.Fatalf("notice count=%d want=%d err=%v", count, want, err)
				}
				if count, err := store.CountPendingKernelResourceWaits(ctx, access); err != nil || count != 1 {
					t.Fatalf("reserved startup disappeared: %d %v", count, err)
				}
				if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || !ok {
					t.Fatal("reserved replay changed", ok, err)
				}
			}
			if deferred {
				notices, err := store.ClaimUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID, "resource-notification-claim", 10)
				if err != nil || len(notices) != 1 || notices[0].ID != "kernel-resources-"+backend.BackendID+"-1" || notices[0].NotificationType != "resource_capacity_admitted" || notices[0].Payload["executed"] != false {
					t.Fatalf("notice differs from existing contract: %#v %v", notices, err)
				}
			}
			if _, err := store.ActivateKernelExecutionBackend(ctx, ActivateKernelExecutionBackendInput{BackendID: backend.BackendID, BackendGeneration: 1, ExecutorInstanceID: backend.ExecutorInstanceID, ExecutorPID: 1101, ExecutorPIDStartTicks: 11, WorkerPID: 1102, WorkerPIDStartTicks: 12, WorkerPGID: 1102, HeartbeatSequence: 1}); err != nil {
				t.Fatal(err)
			}
			if count, err := store.CountPendingKernelResourceWaits(ctx, access); err != nil || count != 0 {
				t.Fatalf("ready idle executor became permanent pending work: %d %v", count, err)
			}
		})
	}
}

func TestKernelResourceAdmissionNotificationFailureRollsBackAndRetries(t *testing.T) {
	store, backend, access := resourceNotificationFixture(t)
	ctx := context.Background()
	capacity := KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1000, MinimumBytes: 100}
	if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER controlled_resource_notice_failure BEFORE INSERT ON notifications BEGIN SELECT RAISE(ABORT,'controlled notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	capacity.AvailableBytes = 1000
	wake := store.OutboxWake()
	if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err == nil || ok {
		t.Fatal("notification failure committed resource promise", ok, err)
	}
	reservation, found, err := store.KernelResourceReservation(ctx, backend.BackendID, 1)
	if err != nil || !found || reservation.State != "waiting" || reservation.ReservedBytes != 0 {
		t.Fatalf("promise did not roll back: %#v %v", reservation, err)
	}
	if count, err := store.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != 0 {
		t.Fatal("partial notice committed", count, err)
	}
	select {
	case <-wake:
		t.Fatal("rolled-back notice emitted a committed wake")
	default:
	}
	if _, err := store.db.Exec(`DROP TRIGGER controlled_resource_notice_failure`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || !ok {
		t.Fatal("notification retry failed", ok, err)
	}
	if count, err := store.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != 1 {
		t.Fatal("retry did not publish exactly once", count, err)
	}
}

func TestKernelResourceAdmissionNotificationSurvivesReopen(t *testing.T) {
	store, backend, access := resourceNotificationFixture(t)
	ctx := context.Background()
	capacity := KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1000, MinimumBytes: 100}
	if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || ok {
		t.Fatal(ok, err)
	}
	capacity.AvailableBytes = 1000
	if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || !ok {
		t.Fatal(ok, err)
	}
	var sequence int
	var name, path string
	if err := store.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, ok, err := reopened.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || !ok {
		t.Fatal("reserved replay lost across reopen", ok, err)
	}
	if count, err := reopened.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != 1 {
		t.Fatal("durable notice missing/duplicated", count, err)
	}
}

func TestKernelResourceDeferredAdmissionSerializesWithCancellation(t *testing.T) {
	for _, order := range []string{"cancel first", "admit first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			store, backend, access := resourceNotificationFixture(t)
			ctx := context.Background()
			capacity := KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1000, MinimumBytes: 100}
			if _, ok, err := store.AdmitKernelResources(ctx, backend, 1000, capacity); err != nil || ok {
				t.Fatal(ok, err)
			}
			capacity.AvailableBytes = 1000
			var admitted bool
			var admissionErr, cancelErr error
			admit := func() { _, admitted, admissionErr = store.AdmitKernelResources(ctx, backend, 1000, capacity) }
			cancel := func() { _, _, cancelErr = store.CancelFrameWithTranscript(ctx, access.Frame.ID) }
			switch order {
			case "cancel first":
				cancel()
				admit()
			case "admit first":
				admit()
				cancel()
			default:
				start := make(chan struct{})
				var done sync.WaitGroup
				done.Add(2)
				go func() { defer done.Done(); <-start; admit() }()
				go func() { defer done.Done(); <-start; cancel() }()
				close(start)
				done.Wait()
			}
			if cancelErr != nil || admissionErr != nil && !errors.Is(admissionErr, ErrKernelExecutionBackendStale) {
				t.Fatalf("admission=%t admission_err=%v cancel_err=%v", admitted, admissionErr, cancelErr)
			}
			if order == "cancel first" && admitted || order == "admit first" && !admitted {
				t.Fatalf("transaction order was not respected: %s admitted=%t", order, admitted)
			}
			want := 0
			if admitted {
				want = 1
			}
			if count, err := store.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != want {
				t.Fatalf("cancel race notice=%d want=%d err=%v", count, want, err)
			}
			if count, err := store.CountPendingKernelResourceWaits(ctx, access); err != nil || count != 0 {
				t.Fatal("cancelled task still pending", count, err)
			}
		})
	}
}
