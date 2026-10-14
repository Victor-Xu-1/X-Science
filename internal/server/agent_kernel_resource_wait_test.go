package server

import (
	"context"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestAgentKernelNotificationWaitIncludesPendingResourceAdmission(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	access := f.identity.access
	spec := workspace.KernelExecutionSessionSpecV1{
		Version: 1, KernelID: "notification-resource-kernel", OwnerUserID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID,
		RootFrameIncarnationID: access.RootFrameIncarnationID, AgentName: "OPERON", KernelKind: "analysis", Language: "python",
		Environment: "controlled", WorkspaceDir: f.projectPath, Mounts: []workspace.KernelExecutionMountSpecV1{}, ProtectedPaths: []string{},
	}
	backend, err := f.store.CreateKernelExecutionBackend(ctx, workspace.CreateKernelExecutionBackendInput{
		BackendID: "notification-resource-backend", OwnerUserID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID,
		RootFrameIncarnationID: access.RootFrameIncarnationID, KernelID: spec.KernelID, KernelGeneration: 1,
		BackendGeneration: 1, ExecutorInstanceID: "notification-resource-executor", MachineBootID: "controlled-resource-boot",
		SocketPath: "/tmp/notification-resource-wait.sock", SessionSpec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	capacity := workspace.KernelResourceCapacity{AvailableBytes: 0, DefaultBytes: 1 << 30, MinimumBytes: 768 << 20}
	if _, admitted, err := f.store.AdmitKernelResources(ctx, backend, 0, capacity); err != nil || admitted {
		t.Fatalf("fixture admitted=%t err=%v", admitted, err)
	}
	pending, hasPending, err := f.server.agentKernelPendingWork(ctx, access)
	if err != nil || !hasPending || numberValue(pending["resource_waits"]) != 1 {
		t.Fatalf("resource wait lost: %#v pending=%t err=%v", pending, hasPending, err)
	}
	result, err := f.server.waitForAgentKernelNotification(ctx, f.identity, 0, "resource-wait-peek")
	if err != nil || result["status"] != "timeout" || numberValue(mapValue(result["pending_work"])["resource_waits"]) != 1 {
		t.Fatalf("resource wait falsely returned idle: %#v err=%v", result, err)
	}
	// The existing resource supervisor admits capacity before issuing this same
	// durable notification. No original cancelled operation is executed here.
	capacity.AvailableBytes = 2 << 30
	if _, admitted, err := f.store.AdmitKernelResources(ctx, backend, 0, capacity); err != nil || !admitted {
		t.Fatalf("capacity admitted=%t err=%v", admitted, err)
	}
	result, err = f.server.waitForAgentKernelNotification(ctx, f.identity, 0, "resource-wait-delivery")
	if err != nil || result["status"] != "received" || numberValue(result["num_notifications"]) != 1 {
		t.Fatalf("durable capacity notification unavailable: %#v err=%v", result, err)
	}
	if !runnerToolCompletionHasMaterialProgress("wait_for_notification", result) {
		t.Fatal("new capacity admission notification stopped being progress")
	}
}

func TestAgentKernelUncollectedLandingsDoNotHideResourceWait(t *testing.T) {
	pending := map[string]any{"uncollected_landings": []any{map[string]any{"frame_id": "completed-child"}}, "resource_waits": 0}
	if !agentKernelHasOnlyUncollectedLandings(pending) {
		t.Fatal("existing uncollected-only result changed")
	}
	pending["resource_waits"] = 1
	if agentKernelHasOnlyUncollectedLandings(pending) {
		t.Fatal("resource admission wait was hidden by a completed child")
	}
}
