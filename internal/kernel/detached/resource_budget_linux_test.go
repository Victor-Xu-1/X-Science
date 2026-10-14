//go:build linux

package detached

import (
	"context"
	"errors"
	"fmt"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

type explicitBudgetTestLauncher struct {
	t         *testing.T
	requested int64
}

func (launcher *explicitBudgetTestLauncher) Launch(ExecutorLaunchRequest) error {
	launcher.t.Error("resource admission must not launch computation")
	return errors.New("unexpected launch")
}

func (launcher *explicitBudgetTestLauncher) Observe(context.Context, string, int64) (ExecutorLaunchState, error) {
	launcher.t.Error("unstarted budget fixture must not observe a process")
	return ExecutorLaunchUnknown, errors.New("unexpected observation")
}

func (launcher *explicitBudgetTestLauncher) ResourceCapacity(_ context.Context, requested int64) (workspace.KernelResourceCapacity, int64, error) {
	launcher.requested = requested
	return workspace.KernelResourceCapacity{
		MaximumBytes: 8 << 30, AvailableBytes: 6 << 30, DefaultBytes: 2 << 30,
		MinimumBytes: executorMemoryFloorBytes, Usage: map[string]int64{},
	}, executorControlReserveBytes, nil
}

func (launcher *explicitBudgetTestLauncher) ResourceUsage(context.Context, string, int64) (int64, ExecutorLaunchState, error) {
	launcher.t.Error("empty reservation inventory must not invent resource use")
	return 0, ExecutorLaunchUnknown, errors.New("unexpected resource observation")
}

func TestExplicitKernelBudgetMinimumIsTypedAndDoesNotReserveOrLaunch(t *testing.T) {
	for _, mib := range []int64{0, 512, 768, 1024} {
		t.Run(fmt.Sprint(mib), func(t *testing.T) {
			store, base := startupBackendFixture(t)
			ctx := context.Background()
			spec, err := workspace.DecodeKernelExecutionSessionSpecV1(base.SessionSpecJSON)
			if err != nil {
				t.Fatal(err)
			}
			spec.KernelID += "-budget"
			spec.ResourceMemoryBytes = mib << 20
			backend, err := store.CreateKernelExecutionBackend(ctx, workspace.CreateKernelExecutionBackendInput{
				BackendID: base.BackendID + "-budget", OwnerUserID: base.OwnerUserID, ProjectID: base.ProjectID,
				RootFrameID: base.RootFrameID, RootFrameIncarnationID: base.RootFrameIncarnationID,
				FrameID: base.FrameID, FrameIncarnationID: base.FrameIncarnationID,
				KernelID: spec.KernelID, KernelGeneration: 1, SessionSpec: spec,
				ExecutorInstanceID: base.ExecutorInstanceID + "-budget", MachineBootID: base.MachineBootID,
				BackendGeneration: 1, SocketPath: base.SocketPath + "budget",
			})
			if err != nil {
				t.Fatal(err)
			}
			launcher := &explicitBudgetTestLauncher{t: t}
			reserved, err := (&Backend{Store: store, Launcher: launcher}).reserveExecutorResources(ctx, backend)
			if launcher.requested != mib<<20 {
				t.Fatal("explicit allocation changed before capacity observation")
			}
			items, listErr := store.ListKernelResourceReservations(ctx, base.MachineBootID)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if mib == 512 {
				var belowMinimum *ExecutorResourceBudgetBelowMinimumError
				if !errors.As(err, &belowMinimum) || belowMinimum.RequestedBytes != 512<<20 ||
					belowMinimum.MinimumBytes != executorMemoryFloorBytes || belowMinimum.BackendID != backend.BackendID ||
					belowMinimum.Generation != backend.BackendGeneration || reserved != 0 || len(items) != 0 {
					t.Fatalf("below-floor request became generic error/wait/reservation: %d %#v %v %v", reserved, belowMinimum, items, err)
				}
				// The lower store authority continues to reject under-minimum input;
				// only the backend maps a valid resource observation to a typed cause.
				capacity, _, capacityErr := launcher.ResourceCapacity(ctx, mib<<20)
				if capacityErr != nil {
					t.Fatal(capacityErr)
				}
				if _, _, storeErr := store.AdmitKernelResources(ctx, backend, mib<<20, capacity); storeErr == nil {
					t.Fatal("typed feedback weakened the durable reservation floor")
				}
			} else {
				want := mib << 20
				if mib == 0 {
					want = 2 << 30
				}
				if err != nil || reserved != want || len(items) != 1 || items[0].ReservedBytes != want ||
					items[0].RequestedBytes != mib<<20 || items[0].State != "reserved" {
					t.Fatalf("default/explicit allocation changed: %d %v %v", reserved, items, err)
				}
			}
			current, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
			if err != nil || !found || current.State != workspace.KernelExecutionBackendStateStarting || current.BackendGeneration != backend.BackendGeneration || current.ExecutorPID != 0 {
				t.Fatalf("resource preflight launched or replaced an executor: %#v %v", current, err)
			}
		})
	}
}
