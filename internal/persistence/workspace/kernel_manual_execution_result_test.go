package workspace

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManualKernelCompletionCommitsLogStateAndOutboxAtomically(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	completion := ManualKernelCompletionInput{Expected: record, Disposition: "completed", FinishedAt: started.Add(time.Millisecond), Log: &ExecutionLogRecord{
		ID: record.ExecutionID, FrameID: record.FrameID, CellIndex: 1, KernelID: record.KernelID, KernelKind: record.KernelKind,
		CondaEnv: record.Request.Environment, Language: record.Request.Language, Source: record.Request.Source,
		Stdout: "frozen\n", ExitStatus: "ok", Origin: "user", ExecutedAt: started,
	}}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_manual_done BEFORE INSERT ON workspace_outbox
		WHEN NEW.event_type='execution_cell_update' AND json_extract(NEW.payload_json,'$.event.payload.phase')='done'
		BEGIN SELECT RAISE(ABORT,'injected manual terminal delivery failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteManualKernelExecution(context.Background(), completion); err == nil {
		t.Fatal("injected outbox failure was ignored")
	}
	current, found, err := store.GetManualKernelExecution(context.Background(), access, record.ExecutionID)
	if err != nil || !found || current.State != "queued" || current.StateVersion != 1 {
		t.Fatalf("partial lifecycle commit: %#v %v", current, err)
	}
	logs, err := store.ListExecutionLog(record.FrameID, "")
	if err != nil || len(logs) != 0 {
		t.Fatalf("partial execution log: %#v %v", logs, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE event_type='execution_cell_update'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial outbox count=%d err=%v", count, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_manual_done`); err != nil {
		t.Fatal(err)
	}
	terminal, err := store.CompleteManualKernelExecution(context.Background(), completion)
	if err != nil || terminal.State != "terminal" || terminal.ExecutionLogID != record.ExecutionID {
		t.Fatalf("terminal=%#v err=%v", terminal, err)
	}
	again, err := store.CompleteManualKernelExecution(context.Background(), completion)
	if err != nil || again.StateVersion != terminal.StateVersion {
		t.Fatalf("terminal replay=%#v err=%v", again, err)
	}
	logs, err = store.ListExecutionLog(record.FrameID, "")
	if err != nil || len(logs) != 1 || logs[0].Stdout != "frozen\n" {
		t.Fatalf("real log=%#v err=%v", logs, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE event_type='execution_cell_update'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("start/done outbox count=%d err=%v", count, err)
	}
	changed := *completion.Log
	changed.Stdout = "different result"
	completion.Log = &changed
	if _, err := store.CompleteManualKernelExecution(context.Background(), completion); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("changed terminal result accepted: %v", err)
	}
}

func TestManualKernelPrestartRejectionPersistsDiagnosticWithoutFakeLog(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	completion := ManualKernelCompletionInput{
		Expected: record, Disposition: "rejected_before_start", FinishedAt: time.Now().UTC(),
		Rejection: &ManualKernelRejection{Stderr: "SyntaxError: invalid syntax"},
	}
	terminal, err := store.CompleteManualKernelExecution(context.Background(), completion)
	if err != nil || terminal.StartedAt != nil || terminal.ExecutionLogID != "" || terminal.Rejection == nil || terminal.Rejection.Stderr != completion.Rejection.Stderr {
		t.Fatalf("prestart rejection=%#v err=%v", terminal, err)
	}
	again, err := store.CompleteManualKernelExecution(context.Background(), completion)
	if err != nil || again.StateVersion != terminal.StateVersion {
		t.Fatalf("rejection replay=%#v err=%v", again, err)
	}
	logs, err := store.ListExecutionLog(access.Frame.ID, "")
	if err != nil || len(logs) != 0 {
		t.Fatalf("preflight invented execution log=%#v err=%v", logs, err)
	}
	var count int
	var phase, stderr string
	if err := store.db.QueryRow(`SELECT COUNT(*),json_extract(payload_json,'$.event.payload.phase'),json_extract(payload_json,'$.event.payload.stderr')
		FROM workspace_outbox WHERE event_type='execution_cell_update'`).Scan(&count, &phase, &stderr); err != nil || count != 1 || phase != "done" || stderr != completion.Rejection.Stderr {
		t.Fatalf("rejection outbox count=%d phase=%q stderr=%q err=%v", count, phase, stderr, err)
	}
	completion.Rejection = &ManualKernelRejection{Stderr: "different error"}
	if _, err := store.CompleteManualKernelExecution(context.Background(), completion); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("different preflight result replaced receipt: %v", err)
	}
}
