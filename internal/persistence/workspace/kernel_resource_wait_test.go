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
		finish       bool
		recreate     bool
		want         int
	}{
		{name: "owned waiting", want: 1},
		{name: "foreign owner", changeAccess: func(a *KernelFrameAccess) { a.UserID = "foreign" }},
		{name: "foreign frame", changeAccess: func(a *KernelFrameAccess) { a.Frame.ID = "other" }},
		{name: "foreign root", changeAccess: func(a *KernelFrameAccess) { a.Frame.RootFrameID = "other" }},
		{name: "old access frame", changeAccess: func(a *KernelFrameAccess) { a.Frame.IncarnationID = "old" }},
		{name: "old access root", changeAccess: func(a *KernelFrameAccess) { a.RootFrameIncarnationID = "old" }},
		{name: "old generation", finish: true, recreate: true},
		{name: "old reservation boot", alter: `UPDATE kernel_resource_reservations SET machine_boot_id='old-boot'`},
		{name: "old current frame", alter: `UPDATE frames SET incarnation_id='new-incarnation' WHERE id='frame'`},
		{name: "cancelled task", alter: `UPDATE frames SET status='cancelled' WHERE id='frame'`},
		{name: "paused task", alter: `UPDATE frames SET status='awaiting_user_response' WHERE id='frame'`},
		{name: "released reservation", alter: `UPDATE kernel_resource_reservations SET state='released'`},
		{name: "terminal backend", finish: true},
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
			if test.finish {
				// This controlled fixture never launched a process. Record its
				// actual startup failure before exercising the terminal guard.
				if err := store.FailKernelExecutorStartup(ctx, KernelStartupFailure{
					BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration,
					ExecutorInstanceID: backend.ExecutorInstanceID, Stage: "launch",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if test.recreate {
				next, err := store.RecreateKernelExecutionBackend(ctx, RecreateKernelExecutionBackendInput{
					BackendID: backend.BackendID, ExecutorInstanceID: backend.ExecutorInstanceID + "-next",
					MachineBootID: backend.MachineBootID, SocketPath: backend.SocketPath,
					KernelID: backend.KernelID, KernelGeneration: backend.KernelGeneration,
					SessionSpec: kernelExecutionSessionSpecForTest(op),
				})
				if err != nil || next.BackendGeneration != backend.BackendGeneration+1 || next.State != KernelExecutionBackendStateStarting {
					t.Fatalf("next generation was not legally recreated: %#v err=%v", next, err)
				}
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
