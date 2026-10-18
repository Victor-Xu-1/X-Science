package server

import (
	"context"
	"database/sql"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func newManualNotebookHTTPRuntime(t *testing.T) (*workspace.Store, *kernelruntime.Manager, http.Handler, workspace.KernelFrameAccess) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("existing confined kernel fixture requires Linux; no behavior was exercised")
	}
	store, manager, app := newKernelAPITestRuntime(t)
	createKernelAPIProjectAndFrame(t, store, "manual-project", "manual-owner", "manual-frame", "OPERON", "")
	access, found, err := store.GetKernelFrameAccess("manual-frame")
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	if _, err := manager.StartSession(kernelruntime.SessionSpec{
		KernelID: "manual-kernel", OwnerID: access.UserID, ProjectID: access.Frame.ProjectID,
		FrameID: access.Frame.ID, FrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		AgentName: "OPERON", Language: "python", Environment: "python", WorkspaceDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	return store, manager, app, access
}

func waitForManualNotebookTerminal(t *testing.T, store *workspace.Store, access workspace.KernelFrameAccess, id string) workspace.ManualKernelExecution {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, found, err := store.GetManualKernelExecution(context.Background(), access, id)
		if err != nil {
			t.Fatal(err)
		}
		if found && record.State == "terminal" {
			return record
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manual execution did not persist its terminal receipt")
	return workspace.ManualKernelExecution{}
}

func TestManualNotebookPreflightRejectionPublishesWithoutExecutionLog(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	response := kernelAPIRequest(t, app, http.MethodPost, "/api/frames/manual-frame/kernel-exec", access.UserID, map[string]any{
		"language": "python", "environment": "python", "code": "return",
	}, http.StatusOK)
	id, _ := response["exec_id"].(string)
	record := waitForManualNotebookTerminal(t, store, access, id)
	if record.Disposition != "rejected_before_start" || record.StartedAt != nil || record.ExecutionLogID != "" || record.Rejection == nil || !strings.Contains(record.Rejection.Stderr, "outside function") {
		t.Fatalf("real preflight receipt=%#v", record)
	}
	events := waitForKernelAPIEvents(t, store, access.UserID, access.Frame.RootFrameID, 1)
	if len(events) != 1 || events[0].Payload["phase"] != "done" || events[0].Payload["exit_code"] != float64(-1) || events[0].Payload["stderr"] != record.Rejection.Stderr {
		t.Fatalf("preflight delivery=%#v", events)
	}
	logs, err := store.ListExecutionLog(access.Frame.ID, "")
	if err != nil || len(logs) != 0 {
		t.Fatalf("preflight invented execution log=%#v err=%v", logs, err)
	}
	deadline := time.Now().Add(time.Second)
	for manager.ActiveExecutionCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("persisted rejection did not acknowledge the retained handle")
	}
}

func TestManualNotebookTerminalPersistenceReleasesRetiredScope(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_manual_log BEFORE INSERT ON execution_log
			BEGIN SELECT RAISE(ABORT,'manual terminal persistence deliberately unavailable'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	response := kernelAPIRequest(t, app, http.MethodPost, "/api/frames/manual-frame/kernel-exec", access.UserID, map[string]any{
		"language": "python", "environment": "python", "code": "print('real result awaiting persistence')",
	}, http.StatusOK)
	id, _ := response["exec_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	persisting := false
	for time.Now().Before(deadline) && !persisting {
		for _, stream := range manager.ListExecStreams(access.Frame.ID) {
			if stream.ExecID == id && stream.Status == "persisting" {
				persisting = true
			}
		}
		if !persisting {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !persisting {
		t.Fatal("fixture did not reach real terminal persistence pending")
	}
	if logs, err := store.ListExecutionLog(access.Frame.ID, ""); err != nil || len(logs) != 0 {
		t.Fatalf("failed persistence created log=%#v err=%v", logs, err)
	}
	if _, err := store.DeleteCompatibilityFrameTree(access.Frame.ID, access.UserID, access.Frame.IncarnationID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for manager.ActiveExecutionCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("deleted original scope retained its terminal persistence handle")
	}
}
