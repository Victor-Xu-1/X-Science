package server

import (
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
)

func TestManualNotebookExistingSubmitHasDurableQueuedAdmissionBeforeExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the existing real kernel confinement fixture is unavailable on Windows; no admission behavior was exercised")
	}
	store, manager, app := newKernelAPITestRuntime(t)
	createKernelAPIProjectAndFrame(t, store, "manual-project", "manual-owner", "manual-root", "OPERON", "")
	access, found, err := store.GetKernelFrameAccess("manual-root")
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	workspaceDir := t.TempDir()
	if _, err := manager.StartSession(kernelruntime.SessionSpec{
		KernelID: "manual-kernel", OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		AgentName: "OPERON", Language: "python", Environment: "python", WorkspaceDir: workspaceDir,
	}); err != nil {
		t.Fatal(err)
	}
	blocker, err := manager.Submit(kernelruntime.SubmitRequest{
		KernelID: "manual-kernel", FrameID: access.Frame.ID,
		OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameIncarnationID: access.Frame.IncarnationID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		Language: "python", Environment: "python", ExecID: "manual-fixture-blocker", ToolUseID: "fixture-blocker", Origin: "user",
		Code: "from pathlib import Path\nimport time\nPath('manual-blocker-started').write_text('ready')\ntime.sleep(5)",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.InterruptSession(access.Frame.ID, access.Frame.IncarnationID, access.RootFrameIncarnationID, "manual-fixture-blocker")
		select {
		case <-blocker.Done():
		case <-time.After(3 * time.Second):
			t.Error("fixture blocker did not stop")
		}
	})
	waitForKernelAPIFile(t, filepath.Join(workspaceDir, "manual-blocker-started"))
	response := kernelAPIRequest(t, app, http.MethodPost, "/api/frames/manual-root/kernel-exec", access.UserID, map[string]any{
		"language": "python", "environment": "python", "code": "print('manual queued request')",
	}, http.StatusOK)
	executionID, _ := response["exec_id"].(string)
	if executionID == "" {
		t.Fatalf("missing actual execution identity: %#v", response)
	}
	t.Cleanup(func() {
		manager.InterruptSession(access.Frame.ID, access.Frame.IncarnationID, access.RootFrameIncarnationID, executionID)
	})
	queued := false
	for _, stream := range manager.ListExecStreams(access.Frame.ID) {
		if stream.ExecID == executionID {
			queued = stream.Status == "queued"
		}
	}
	if !queued {
		t.Fatal("fixture did not hold the actual manual request in the queued window")
	}
	logs, err := store.ListExecutionLog(access.Frame.ID, "")
	if err != nil || len(logs) != 0 {
		t.Fatalf("queued request invented execution history: %#v err=%v", logs, err)
	}
	events, err := store.ListFrameEvents(access.Frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "kernel_manual_execution" && event.Payload["execution_id"] == executionID {
			return
		}
	}
	t.Fatal("actual manual POST returned an accepted execution id without durable queued admission")
}
