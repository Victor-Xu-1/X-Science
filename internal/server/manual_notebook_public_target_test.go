package server

import (
	"context"
	"net/http"
	"os"
	"testing"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualNotebookPublicTargetUsesActualLocalRegistrationIdentity(t *testing.T) {
	_, manager, app, access := newManualNotebookHTTPRuntime(t)
	frozen := manualPublicRequestBody(t, manager, access)
	response := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/manual-frame/kernels", access.UserID, nil, http.StatusOK)
	kernels, ok := response["kernels"].([]any)
	if !ok || len(kernels) != 1 {
		t.Fatalf("fixture inventory=%#v", response)
	}
	kernel := kernels[0].(map[string]any)
	target, ok := kernel["manual_execution_target"].(map[string]any)
	if !ok {
		t.Fatalf("live owned kernel lacks typed manual target: %#v", kernel)
	}
	if target["schema"] != "x-science.manual-kernel-target.v1" ||
		target["frame_id"] != access.Frame.ID || target["root_frame_id"] != access.Frame.RootFrameID ||
		target["frame_incarnation_id"] != access.Frame.IncarnationID || target["root_frame_incarnation_id"] != access.RootFrameIncarnationID ||
		target["kernel_id"] != "manual-kernel" || target["kernel_generation"] != float64(frozen["expected_kernel_generation"].(uint64)) ||
		target["language"] != "python" || target["environment"] != "python" {
		t.Fatalf("target does not match registered scope/generation: %#v", target)
	}
	// A different account cannot obtain a target it could replay as its own.
	kernelAPIRequest(t, app, http.MethodGet, "/api/frames/manual-frame/kernels", "different-owner", nil, http.StatusNotFound)
}

func TestManualNotebookPublicDetachedInventoryDoesNotAdvertiseLocalSubmit(t *testing.T) {
	store, _, app := newKernelAPITestRuntime(t)
	createKernelAPIProjectAndFrame(t, store, "manual-project", "manual-owner", "manual-frame", "OPERON", "")
	access, found, err := store.GetKernelFrameAccess("manual-frame")
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	ticks, err := kernelruntime.CurrentProcessStartTicks()
	if err != nil {
		t.Fatal(err)
	}
	backend, err := store.CreateKernelExecutionBackend(context.Background(), workspace.CreateKernelExecutionBackendInput{
		BackendID: "manual-detached-backend", OwnerUserID: access.UserID, ProjectID: access.Frame.ProjectID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID,
		KernelID: "manual-detached-kernel", KernelGeneration: 1, ExecutorInstanceID: "manual-detached-executor",
		MachineBootID: "manual-test-boot", SocketPath: "/run/user/1000/manual-detached.sock", BackendGeneration: 1,
		SessionSpec: workspace.KernelExecutionSessionSpecV1{
			Version: 1, KernelID: "manual-detached-kernel", OwnerUserID: access.UserID, ProjectID: access.Frame.ProjectID,
			RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID,
			FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID, AgentName: "OPERON",
			KernelKind: "analysis", Language: "python", Environment: "python", WorkspaceDir: t.TempDir(),
			Mounts: []workspace.KernelExecutionMountSpecV1{}, ProtectedPaths: []string{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateKernelExecutionBackend(context.Background(), workspace.ActivateKernelExecutionBackendInput{
		BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID,
		ExecutorPID: int64(os.Getpid()), ExecutorPIDStartTicks: ticks, WorkerPID: int64(os.Getpid()), WorkerPIDStartTicks: ticks,
		WorkerPGID: int64(os.Getpid()), CgroupPath: "/x-science-test.scope", HeartbeatSequence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// This uses the existing real PID-probe inventory fixture, not a running
	// detached computation, and checks only the public capability projection.
	response := kernelAPIRequest(t, app, http.MethodGet, "/api/frames/manual-frame/kernels", access.UserID, nil, http.StatusOK)
	kernels, ok := response["kernels"].([]any)
	if !ok || len(kernels) != 1 {
		t.Fatalf("detached inventory=%#v", response)
	}
	kernel := kernels[0].(map[string]any)
	if kernel["kernel_id"] != "manual-detached-kernel" || kernel["manual_execution_target"] != nil {
		t.Fatalf("detached inventory acquired local Manager submission authority: %#v", kernel)
	}
}
