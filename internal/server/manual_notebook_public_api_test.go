package server

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualNotebookPublicReplayPrecedesMutableKernelChecks(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	body := manualPublicRequestBody(t, manager, access)
	accepted := kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusAccepted)
	first, _ := manualPublicReceipt(t, accepted, body)
	terminal := waitForManualNotebookTerminal(t, store, access, first.ExecutionID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := manager.CloseAll(ctx); err != nil {
		t.Fatal(err)
	}
	// The original target no longer exists. A lost ACK retry must still return
	// the same completed receipt, without creating or selecting a replacement.
	replayed := kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusOK)
	again, _ := manualPublicReceipt(t, replayed, body)
	if again.ExecutionID != first.ExecutionID || again.StateVersion != terminal.StateVersion || again.State != "terminal" {
		t.Fatalf("terminal replay changed execution: %#v", again)
	}
	lookup := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), access.UserID, nil, http.StatusOK)
	queried, _ := manualPublicReceipt(t, lookup, body)
	if queried.ExecutionID != first.ExecutionID || queried.ExecutionLogID != terminal.ExecutionLogID {
		t.Fatalf("GET did not recover the original receipt: %#v", queried)
	}
	wrongTarget := manualPublicCopyBody(body)
	wrongTarget["expected_kernel_generation"] = body["expected_kernel_generation"].(uint64) + 1
	kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(wrongTarget), access.UserID, nil, http.StatusConflict)
	kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), "different-owner", nil, http.StatusNotFound)
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["code"] = "print('different')" },
		func(v map[string]any) {
			v["expected_kernel_generation"] = body["expected_kernel_generation"].(uint64) + 1
		},
		func(v map[string]any) { v["expected_frame_incarnation_id"] = "different-incarnation" },
	} {
		changed := manualPublicCopyBody(body)
		mutate(changed)
		kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, changed, http.StatusConflict)
	}
	logs, err := store.ListExecutionLog(access.Frame.ID, "")
	if err != nil || len(logs) != 1 || logs[0].Stdout != "typed once\n" || manager.ActiveExecutionCount() != 0 {
		t.Fatalf("retry duplicated execution: logs=%#v active=%d err=%v", logs, manager.ActiveExecutionCount(), err)
	}
}

func TestManualNotebookPublicCancelRequiresAcceptedTypedReceipt(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	body := manualPublicRequestBody(t, manager, access)
	id := workspace.ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, body["request_id"].(string))
	gate := make(chan error, 1)
	handle, err := manager.Submit(kernelruntime.SubmitRequest{
		OwnerID: access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
		FrameIncarnationID: access.Frame.IncarnationID, RootFrameIncarnationID: access.RootFrameIncarnationID,
		KernelID: "manual-kernel", ExpectedGeneration: body["expected_kernel_generation"].(uint64),
		Language: "python", Environment: "python", ExecID: id, ToolUseID: "user-" + id,
		Code: body["code"].(string), Origin: "user", StartAuthorization: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.InterruptSession(access.Frame.ID, access.Frame.IncarnationID, access.RootFrameIncarnationID, id)
		close(gate)
		select {
		case <-handle.Done():
		case <-time.After(time.Second):
			t.Error("test registration did not close")
		}
	})
	response := kernelAPIRequest(t, app, http.MethodPost, manualPublicCancelURL(body), access.UserID, body, http.StatusConflict)
	if response["code"] != "MANUAL_EXECUTION_NOT_ACCEPTED" {
		t.Fatalf("missing receipt cancellation=%#v", response)
	}
	streams := manager.ListExecStreams(access.Frame.ID)
	if len(streams) != 1 || streams[0].ExecID != id || streams[0].Status != "queued" {
		t.Fatalf("typed cancellation fell back to legacy Manager entry: %#v", streams)
	}
	if _, found, err := store.GetManualKernelExecution(context.Background(), access, id); err != nil || found {
		t.Fatalf("absent cancel invented admission/tombstone found=%t err=%v", found, err)
	}
}

func TestManualNotebookPublicObservationDoesNotPromoteOldBootQueuedReceipt(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	body := manualPublicRequestBody(t, manager, access)
	request := workspace.ManualKernelRequest{
		RequestID: body["request_id"].(string), Source: body["code"].(string), Language: "python", Environment: "python",
		KernelID: "manual-kernel", ExpectedKernelGeneration: body["expected_kernel_generation"].(uint64), TimeoutMillis: body["timeout_ms"].(int64),
	}
	original, _, err := store.AdmitManualKernelExecution(context.Background(), workspace.AdmitManualKernelExecutionInput{
		Request: request, Binding: workspace.ManualKernelBinding{
			ExecutionID: workspace.ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID),
			OwnerID:     access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
			FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID, RootIncarnationID: access.RootFrameIncarnationID,
			KernelID: request.KernelID, KernelGeneration: request.ExpectedKernelGeneration, KernelKind: "analysis", ServerBootID: "previous-boot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), access.UserID, nil, http.StatusOK)
	durable, physical := manualPublicReceipt(t, response, body)
	if durable.State != "queued" || durable.StateVersion != original.StateVersion || physical["status"] != "unavailable" {
		t.Fatalf("persisted queued became physical running/stopped: durable=%#v physical=%#v", durable, physical)
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("read-only observation dispatched old code")
	}
	cancelResponse := kernelAPIRequest(t, app, http.MethodPost, manualPublicCancelURL(body), access.UserID, body, http.StatusOK)
	cancelled, physical := manualPublicReceipt(t, cancelResponse, body)
	if cancelled.State != "cancel_requested" || cancelled.FinishedAt != nil || physical["status"] != "unavailable" || manager.ActiveExecutionCount() != 0 {
		t.Fatalf("new boot cancellation fabricated physical stop: durable=%#v physical=%#v", cancelled, physical)
	}
}

func TestManualNotebookPublicObservationAndCancelUseOwnedExecution(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	body := manualPublicRequestBody(t, manager, access)
	body["code"] = "import time\nwhile True:\n    time.sleep(0.01)"
	response := kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusAccepted)
	record, _ := manualPublicReceipt(t, response, body)
	t.Cleanup(func() {
		manager.InterruptSession(access.Frame.ID, access.Frame.IncarnationID, access.RootFrameIncarnationID, record.ExecutionID)
	})
	deadline := time.Now().Add(3 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		current, found, err := store.GetManualKernelExecution(context.Background(), access, record.ExecutionID)
		if err != nil {
			t.Fatal(err)
		}
		if found && current.StartedAt != nil {
			started = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started {
		t.Fatal("real worker did not acknowledge the owned execution")
	}
	observed := kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), access.UserID, nil, http.StatusOK)
	_, physical := manualPublicReceipt(t, observed, body)
	if physical["status"] != "running" {
		t.Fatalf("matching live Manager registration was not observed: %#v", physical)
	}
	requested := kernelAPIRequest(t, app, http.MethodPost, manualPublicCancelURL(body), access.UserID, body, http.StatusOK)
	cancelling, _ := manualPublicReceipt(t, requested, body)
	if cancelling.CancelRequestedAt == nil || cancelling.State != "cancel_requested" && cancelling.State != "terminal" {
		t.Fatalf("cancel did not record monotonic intent: %#v", cancelling)
	}
	terminal := waitForManualNotebookTerminal(t, store, access, record.ExecutionID)
	if terminal.Disposition != "cancelled" || terminal.ExecutionLogID != record.ExecutionID {
		t.Fatalf("actual cancellation lacked persisted terminal evidence: %#v", terminal)
	}
}

func TestManualNotebookPublicObservationDistinguishesPersistencePending(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	body := manualPublicRequestBody(t, manager, access)
	if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER block_public_manual_result BEFORE INSERT ON execution_log
			BEGIN SELECT RAISE(ABORT,'public manual result persistence pending'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	removeBlock := func() {
		t.Helper()
		if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`DROP TRIGGER IF EXISTS block_public_manual_result`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(removeBlock)
	response := kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusAccepted)
	record, _ := manualPublicReceipt(t, response, body)
	deadline := time.Now().Add(3 * time.Second)
	pending := false
	for time.Now().Before(deadline) && !pending {
		for _, stream := range manager.ListExecStreams(access.Frame.ID) {
			if stream.ExecID == record.ExecutionID && stream.Status == "persisting" {
				pending = true
			}
		}
		if !pending {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !pending {
		t.Fatal("fixture did not reach persistence pending")
	}
	response = kernelAPIRequest(t, app, http.MethodGet, manualPublicLookupURL(body), access.UserID, nil, http.StatusOK)
	durable, physical := manualPublicReceipt(t, response, body)
	if durable.State == "terminal" || physical["status"] != "persisting" {
		t.Fatalf("pending result became durable success or active execution: durable=%#v physical=%#v", durable, physical)
	}
	if logs, err := store.ListExecutionLog(access.Frame.ID, ""); err != nil || len(logs) != 0 {
		t.Fatalf("uncommitted result log=%#v err=%v", logs, err)
	}
	removeBlock()
	terminal := waitForManualNotebookTerminal(t, store, access, record.ExecutionID)
	if terminal.ExecutionLogID != record.ExecutionID {
		t.Fatalf("actual result failed to settle: %#v", terminal)
	}
}
