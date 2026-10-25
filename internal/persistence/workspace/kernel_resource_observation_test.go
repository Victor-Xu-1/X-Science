package workspace

import (
	"context"
	"testing"

	"x-science/internal/toolprogress"
)

func otherResourceObservationBackend(t *testing.T, store *Store, original KernelExecutionBackend) KernelExecutionBackend {
	t.Helper()
	spec, err := DecodeKernelExecutionSessionSpecV1(original.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	spec.KernelID = "resource-observation-target-kernel"
	backend, err := store.CreateKernelExecutionBackend(context.Background(), CreateKernelExecutionBackendInput{
		BackendID: "resource-observation-target", OwnerUserID: original.OwnerUserID, ProjectID: original.ProjectID,
		FrameID: original.FrameID, FrameIncarnationID: original.FrameIncarnationID,
		RootFrameID: original.RootFrameID, RootFrameIncarnationID: original.RootFrameIncarnationID,
		KernelID: spec.KernelID, KernelGeneration: 1, BackendGeneration: 1,
		ExecutorInstanceID: "resource-observation-executor", MachineBootID: original.MachineBootID,
		SocketPath: "/tmp/x-science-resource-observation.sock", SessionSpec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func TestKernelResourceAdmissionObservesSerializedDecisionAndDurableNotice(t *testing.T) {
	for _, fairness := range []bool{false, true} {
		t.Run(map[bool]string{false: "unused promises", true: "earlier waiter"}[fairness], func(t *testing.T) {
			ctx := context.Background()
			store, original, access := resourceNotificationFixture(t)
			capacity := KernelResourceCapacity{MaximumBytes: 10000, AvailableBytes: 1000, DefaultBytes: 1000, MinimumBytes: 100}
			if fairness {
				capacity.AvailableBytes = 0
			}
			if _, admitted, err := store.AdmitKernelResources(ctx, original, 800, capacity); err != nil || admitted == fairness {
				t.Fatal("original reservation fixture differs", admitted, err)
			}
			target := otherResourceObservationBackend(t, store, original)
			capacity.AvailableBytes = 900
			capacity.Usage = map[string]int64{KernelResourceUsageKey(original.BackendID, original.BackendGeneration): 200}
			if fairness {
				capacity.AvailableBytes = 2000
			}
			waiting, admitted, err := store.AdmitKernelResources(ctx, target, 400, capacity)
			if err != nil || admitted || waiting.State != "waiting" || waiting.RequestedBytes != 400 || waiting.ReservedBytes != 0 || waiting.Admission == nil {
				t.Fatalf("observation changed admission: %+v admitted=%t err=%v", waiting, admitted, err)
			}
			observation := waiting.Admission
			if fairness {
				if observation.Reason != toolprogress.ResourceAdmissionFairness || observation.PrecedingWaiters != 1 || observation.UnusedPromisedBytes != 0 || observation.NetAvailableBytes != 2000 {
					t.Fatalf("fairness decision was not captured consistently: %+v", observation)
				}
			} else if observation.Reason != toolprogress.ResourceAdmissionCapacity || observation.PrecedingWaiters != 0 || observation.CapacityAvailableBytes != 900 || observation.UnusedPromisedBytes != 600 || observation.NetAvailableBytes != 300 || observation.RequiredBytes != 400 {
				t.Fatalf("unused promise decision was not captured consistently: %+v", observation)
			}
			if count, err := store.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != 0 {
				t.Fatal("waiting telemetry created a new notification/model input", count, err)
			}
			// Both are controlled DB-only fixtures: no process was ever launched.
			if err := store.ReleaseKernelResources(ctx, original.BackendID, original.BackendGeneration, original.MachineBootID); err != nil {
				t.Fatal(err)
			}
			capacity.AvailableBytes = 900
			granted, admitted, err := store.AdmitKernelResources(ctx, target, 400, capacity)
			if err != nil || !admitted || granted.Admission == nil || granted.ReservedBytes != 400 || granted.Admission.Reason != toolprogress.ResourceAdmissionGranted {
				t.Fatalf("original admission did not carry its decision: %+v %t %v", granted, admitted, err)
			}
			decisionAt := granted.Admission.DecisionAt
			for range 3 {
				capacity.AvailableBytes = 0
				if _, ok, err := store.AdmitKernelResources(ctx, target, 400, capacity); err != nil || !ok {
					t.Fatal("reserved replay changed allocation", ok, err)
				}
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
			t.Cleanup(func() { _ = reopened.Close() })
			readback, found, err := reopened.KernelResourceReservation(ctx, target.BackendID, target.BackendGeneration)
			if err != nil || !found || readback.Admission == nil || readback.Admission.DecisionAt != decisionAt || readback.Admission.CapacityAvailableBytes != 900 || readback.Admission.NetAvailableBytes != 900 || readback.ReservedBytes != 400 {
				t.Fatalf("reopen fabricated current capacity or lost the original decision: %+v found=%t err=%v", readback, found, err)
			}
			if count, err := reopened.CountUnreadNotifications(ctx, access.Frame.ID, access.Frame.RootFrameID, access.UserID); err != nil || count != 1 {
				t.Fatal("admitted evidence was lost or duplicated", count, err)
			}
			// A pre-upgrade notice has no observation. It still owns a valid
			// reservation; optional readback must not block or invent history.
			if _, err := reopened.db.Exec(`UPDATE notifications SET payload_json=json_remove(payload_json,'$.resource_admission') WHERE id=?`, kernelResourceAdmissionNotificationID(target.BackendID, target.BackendGeneration)); err != nil {
				t.Fatal(err)
			}
			legacy, found, err := reopened.KernelResourceReservation(ctx, target.BackendID, target.BackendGeneration)
			if err != nil || !found || legacy.State != "reserved" || legacy.ReservedBytes != 400 || legacy.Admission != nil {
				t.Fatalf("legacy telemetry changed reservation semantics: %+v found=%t err=%v", legacy, found, err)
			}
		})
	}
}
