package workspace

import (
	"context"
	"testing"
)

func TestPendingKernelResourceWaitsUseCurrentTaskAuthority(t *testing.T) {
	for _, test := range []struct {
		name         string
		alter        string
		changeAccess func(*KernelFrameAccess)
		want         int
	}{
		{name: "owned waiting", want: 1},
		{name: "foreign owner", changeAccess: func(a *KernelFrameAccess) { a.UserID = "foreign" }},
		{name: "foreign frame", changeAccess: func(a *KernelFrameAccess) { a.Frame.ID = "other" }},
		{name: "foreign root", changeAccess: func(a *KernelFrameAccess) { a.Frame.RootFrameID = "other" }},
		{name: "old access frame", changeAccess: func(a *KernelFrameAccess) { a.Frame.IncarnationID = "old" }},
		{name: "old access root", changeAccess: func(a *KernelFrameAccess) { a.RootFrameIncarnationID = "old" }},
		{name: "old generation", alter: `UPDATE kernel_execution_backends SET backend_generation=2 WHERE backend_id='resource-wait-backend'`},
		{name: "old reservation boot", alter: `UPDATE kernel_resource_reservations SET machine_boot_id='old-boot'`},
		{name: "old current frame", alter: `UPDATE frames SET incarnation_id='new-incarnation' WHERE id='frame'`},
		{name: "cancelled task", alter: `UPDATE frames SET status='cancelled' WHERE id='frame'`},
		{name: "paused task", alter: `UPDATE frames SET status='awaiting_user_response' WHERE id='frame'`},
		{name: "released reservation", alter: `UPDATE kernel_resource_reservations SET state='released'`},
		{name: "terminal backend", alter: `UPDATE kernel_execution_backends SET state='stopped' WHERE backend_id='resource-wait-backend'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, repo, claim := newKernelLocalOperationFixture(t)
			op := createKernelLocalOperationForTest(t, store, repo, claim, "resource-wait-source", "resource-wait-call")
			op.KernelID = "resource-wait-kernel"
			backend, err := store.CreateKernelExecutionBackend(ctx, CreateKernelExecutionBackendInput{
				BackendID: "resource-wait-backend", OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID,
				FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID, RootFrameID: op.RootFrameID,
				RootFrameIncarnationID: op.RootFrameIncarnationID, KernelID: op.KernelID, KernelGeneration: 1,
				BackendGeneration: 1, ExecutorInstanceID: "resource-wait-executor", MachineBootID: "resource-wait-boot",
				SocketPath: "/tmp/x-science-resource-wait.sock", SessionSpec: kernelExecutionSessionSpecForTest(op),
			})
			if err != nil {
				t.Fatal(err)
			}
			reservation, admitted, err := store.AdmitKernelResources(ctx, backend, 0, KernelResourceCapacity{
				AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: 768 << 20,
			})
			if err != nil || admitted || reservation.State != "waiting" {
				t.Fatalf("fixture not waiting: %#v %t %v", reservation, admitted, err)
			}
			access, found, err := store.GetKernelFrameAccessContext(ctx, op.FrameID)
			if err != nil || !found {
				t.Fatalf("access found=%t err=%v", found, err)
			}
			if test.changeAccess != nil {
				test.changeAccess(&access)
			}
			if test.alter != "" {
				if _, err := store.db.Exec(test.alter); err != nil {
					t.Fatal(err)
				}
			}
			count, err := store.CountPendingKernelResourceWaits(ctx, access)
			if err != nil || count != test.want {
				t.Fatalf("count=%d want=%d err=%v", count, test.want, err)
			}
		})
	}
}
