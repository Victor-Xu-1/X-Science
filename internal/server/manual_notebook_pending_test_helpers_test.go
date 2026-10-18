package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func newManualPendingTestRuntime(t *testing.T) (*workspace.Store, *kernelruntime.Manager, http.Handler, workspace.KernelFrameAccess) {
	t.Helper()
	store, manager, app := newKernelAPITestRuntime(t)
	createKernelAPIProjectAndFrame(t, store, "pending-project", "pending-owner", "manual-frame", "OPERON", "")
	access, found, err := store.GetKernelFrameAccess("manual-frame")
	if err != nil || !found {
		t.Fatalf("pending fixture access found=%t err=%v", found, err)
	}
	return store, manager, app, access
}

// Historical admission facts use the real typed SQLite store. No live kernel
// is created: these fixtures deliberately model receipts from an older boot.
func admitManualPendingTestRecord(t *testing.T, store *workspace.Store, access workspace.KernelFrameAccess, kernelID string, generation uint64, source string) workspace.ManualKernelExecution {
	t.Helper()
	request := workspace.ManualKernelRequest{RequestID: uuid.NewString(), Source: source, Language: "python", Environment: "python",
		KernelID: kernelID, ExpectedKernelGeneration: generation, TimeoutMillis: 5000}
	return admitManualPendingTestRequest(t, store, access, kernelID, generation, request)
}

func admitManualPendingTestRequest(t *testing.T, store *workspace.Store, access workspace.KernelFrameAccess, kernelID string, generation uint64, request workspace.ManualKernelRequest) workspace.ManualKernelExecution {
	t.Helper()
	record, created, err := store.AdmitManualKernelExecution(context.Background(), workspace.AdmitManualKernelExecutionInput{
		Request: request, Binding: workspace.ManualKernelBinding{
			ExecutionID: workspace.ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID),
			OwnerID:     access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
			FrameIncarnationID: access.Frame.IncarnationID, RootFrameID: access.Frame.RootFrameID, RootIncarnationID: access.RootFrameIncarnationID,
			KernelID: kernelID, KernelGeneration: generation, KernelKind: "analysis", ServerBootID: "old-pending-boot",
		},
	})
	if err != nil || !created {
		t.Fatalf("historical admission created=%t err=%v", created, err)
	}
	return record
}

func appendManualPendingOrdinaryEvents(t *testing.T, store *workspace.Store, frameID string, count int) {
	t.Helper()
	if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
		var sequence int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM frame_events WHERE frame_id=?`, frameID).Scan(&sequence); err != nil {
			return err
		}
		for index := 0; index < count; index++ {
			_, err := tx.Exec(`INSERT INTO frame_events(id,frame_id,sequence,event_type,payload,created_at) VALUES(?,?,?,?,?,CURRENT_TIMESTAMP)`,
				uuid.NewString(), frameID, sequence+int64(index)+1, "frame_activity", `{"text":"ordinary history","code":"ORDINARY_SOURCE_MUST_NOT_LEAK"}`)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func manualPendingTestURL(access workspace.KernelFrameAccess, cursor string) string {
	query := url.Values{
		"expected_frame_incarnation_id":      {access.Frame.IncarnationID},
		"expected_root_frame_id":             {access.Frame.RootFrameID},
		"expected_root_frame_incarnation_id": {access.RootFrameIncarnationID},
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	return "/api/frames/" + url.PathEscape(access.Frame.ID) + "/kernel-executions?" + query.Encode()
}

type manualPendingTestPage struct {
	pending  []any
	next     string
	complete bool
	through  int64
	raw      map[string]any
}

func readManualPendingTestPage(t *testing.T, app http.Handler, access workspace.KernelFrameAccess, cursor string) manualPendingTestPage {
	t.Helper()
	response := kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(access, cursor), access.UserID, nil, http.StatusOK)
	pending, ok := response["pending"].([]any)
	complete, completeOK := response["scan_complete"].(bool)
	through, throughOK := response["through_sequence"].(float64)
	next, nextOK := response["next_cursor"].(string)
	if response["next_cursor"] == nil {
		next, nextOK = "", true
	}
	if response["schema"] != "x-science.manual-kernel-pending.v1" || !ok || !completeOK || !throughOK || !nextOK || len(pending) > 32 || len(response) != 5 || complete != (next == "") {
		t.Fatalf("invalid bounded discovery page: %#v", response)
	}
	return manualPendingTestPage{pending: pending, next: next, complete: complete, through: int64(through), raw: response}
}

func manualPendingRequestBody(record workspace.ManualKernelExecution) map[string]any {
	return map[string]any{"schema": "x-science.manual-kernel-request.v1", "request_id": record.Request.RequestID,
		"expected_frame_incarnation_id": record.FrameIncarnationID, "expected_root_frame_id": record.RootFrameID,
		"expected_root_frame_incarnation_id": record.RootIncarnationID, "kernel_id": record.KernelID,
		"expected_kernel_generation": record.KernelGeneration, "language": record.Request.Language,
		"environment": record.Request.Environment, "code": record.Request.Source, "timeout_ms": record.Request.TimeoutMillis}
}

func assertManualPendingSummary(t *testing.T, value any, record workspace.ManualKernelExecution) {
	t.Helper()
	summary, ok := value.(map[string]any)
	kind := "typed-v1"
	if record.Request.KernelID == "" {
		kind = "legacy"
	}
	if !ok || len(summary) != 7 || summary["request_kind"] != kind || summary["request_id"] != record.Request.RequestID || summary["execution_id"] != record.ExecutionID ||
		summary["state"] != record.State || summary["state_version"] != float64(record.StateVersion) || summary["accepted_at"] == nil {
		t.Fatalf("invalid pending summary=%#v", value)
	}
	target, ok := summary["target"].(map[string]any)
	if !ok || len(target) != 9 || target["schema"] != "x-science.manual-kernel-target.v1" || target["frame_id"] != record.FrameID ||
		target["root_frame_id"] != record.RootFrameID || target["frame_incarnation_id"] != record.FrameIncarnationID || target["root_frame_incarnation_id"] != record.RootIncarnationID ||
		target["kernel_id"] != record.KernelID || target["kernel_generation"] != float64(record.KernelGeneration) || target["language"] != record.Request.Language || target["environment"] != record.Request.Environment {
		t.Fatalf("summary target does not retain original binding: %s", fmt.Sprint(target))
	}
}
