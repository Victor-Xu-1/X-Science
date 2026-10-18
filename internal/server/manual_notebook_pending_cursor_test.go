package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestManualNotebookPendingCursorBindsOwnerAndExactScope(t *testing.T) {
	store, manager, app, access := newManualPendingTestRuntime(t)
	admitManualPendingTestRecord(t, store, access, "old-kernel", 7, "PRIVATE_PRIMARY_SOURCE")
	appendManualPendingOrdinaryEvents(t, store, access.Frame.ID, 128)
	first := readManualPendingTestPage(t, app, access, "")
	if first.next == "" {
		t.Fatal("fixture did not produce a bounded continuation")
	}
	for _, fixture := range []struct{ project, owner, frame string }{
		{"pending-project", "pending-owner", "another-owned-frame"},
		{"foreign-project", "foreign-owner", "foreign-frame"},
	} {
		createKernelAPIProjectAndFrame(t, store, fixture.project, fixture.owner, fixture.frame, "OPERON", "")
		other, found, err := store.GetKernelFrameAccess(fixture.frame)
		if err != nil || !found {
			t.Fatalf("other scope found=%t err=%v", found, err)
		}
		admitManualPendingTestRecord(t, store, other, "other-kernel", 9, "OTHER_FRAME_PRIVATE_SOURCE")
		kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(other, first.next), other.UserID, nil, http.StatusConflict)
	}
	denied := kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(access, first.next), "foreign-owner", nil, http.StatusNotFound)
	encoded, _ := json.Marshal(denied)
	if strings.Contains(string(encoded), "PRIVATE_SOURCE") {
		t.Fatalf("foreign failure leaked source=%s", encoded)
	}
	setOwner := func(owner string) {
		t.Helper()
		if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE projects SET user_id=? WHERE id=?`, owner, access.Frame.ProjectID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setOwner("replacement-owner")
	reowned := access
	reowned.UserID = "replacement-owner"
	kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(reowned, first.next), reowned.UserID, nil, http.StatusConflict)
	setOwner(access.UserID)
	stale := access
	stale.Frame.IncarnationID = "replaced-incarnation"
	kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(stale, first.next), access.UserID, nil, http.StatusConflict)
	stale = access
	stale.RootFrameIncarnationID = "replaced-root-incarnation"
	kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(stale, first.next), access.UserID, nil, http.StatusConflict)
	kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(access, "not-an-opaque-cursor"), access.UserID, nil, http.StatusBadRequest)
	kernelAPIRequest(t, app, http.MethodGet, manualPendingTestURL(access, "")+"&kernel_id=forbidden-filter", access.UserID, nil, http.StatusBadRequest)
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("cursor rejection dispatched code")
	}
}

func TestManualNotebookPendingCursorFreezesAdmissionUpperBoundButReadsCurrentState(t *testing.T) {
	store, manager, app, access := newManualPendingTestRuntime(t)
	old := admitManualPendingTestRecord(t, store, access, "old-kernel", 7, "OLD_PRIVATE_SOURCE")
	appendManualPendingOrdinaryEvents(t, store, access.Frame.ID, 128)
	first := readManualPendingTestPage(t, app, access, "")
	if first.complete || len(first.pending) != 0 {
		t.Fatalf("initial scan=%#v", first.raw)
	}
	cancelling, err := store.RequestManualKernelExecutionCancellation(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := store.CompleteManualKernelExecution(context.Background(), workspace.ManualKernelCompletionInput{
		Expected: cancelling, Disposition: "cancelled_before_start", FinishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	late := admitManualPendingTestRecord(t, store, access, "new-kernel", 8, "LATE_PRIVATE_SOURCE")
	second := readManualPendingTestPage(t, app, access, first.next)
	if !second.complete || len(second.pending) != 0 || second.through != first.through {
		t.Fatalf("cursor included new admission or stale terminal=%#v", second.raw)
	}
	fresh := readManualPendingTestPage(t, app, access, "")
	if fresh.through <= first.through || len(fresh.pending) != 1 {
		t.Fatalf("fresh scan failed to include later admission=%#v", fresh.raw)
	}
	assertManualPendingSummary(t, fresh.pending[0], late)
	current, found, err := store.GetManualKernelExecution(context.Background(), access, old.ExecutionID)
	if err != nil || !found || current.State != "terminal" || current.StateVersion != terminal.StateVersion {
		t.Fatalf("discovery changed terminal=%#v err=%v", current, err)
	}
	if logs, err := store.ListExecutionLog(access.Frame.ID, ""); err != nil || len(logs) != 0 || manager.ActiveExecutionCount() != 0 {
		t.Fatalf("read-only scan created execution/result logs=%#v err=%v", logs, err)
	}
}
