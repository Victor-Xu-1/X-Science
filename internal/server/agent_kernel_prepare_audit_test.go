package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelPrepareAuditUsesCapturedBindingAndExistingWriteOnly(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	srv := &Server{runtimeStore: store}
	run := &sessionRunnerChatRun{SessionID: "session", Attempt: 3, KernelOperationIDs: map[string]string{"call": "operation"}, Transcript: &transcriptRunnerAuthority{Claim: transcriptstore.RunnerClaim{Attempt: 7}}}
	ctx, timing := srv.withAgentKernelPrepareTiming(withTranscriptRunnerChatRun(context.Background(), run), &agentruntime.ToolCall{ID: "call"})
	if timing == nil || agentKernelPrepareTimingFromContext(ctx) != timing {
		t.Fatal("original checkpoint binding did not reach preparation")
	}
	timing.begin(kernelPrepareDurable)()
	timing.finish("prepared")
	run.KernelOperationIDs["call"] = "later-operation"
	if entries, err := store.List(sessionRunnerCommunicationAuditNamespace); err != nil || len(entries) != 0 {
		t.Fatalf("sealing caused an extra database write: %v %v", entries, err)
	}
	srv.recordSessionRunnerCommunicationAudit(context.Background(), run, map[string]any{"decision": "existing-model-boundary"})
	entries, err := store.List(sessionRunnerCommunicationAuditNamespace)
	if err != nil || len(entries) != 1 {
		t.Fatalf("original audit write count changed: %d %v", len(entries), err)
	}
	raw, _ := json.Marshal(entries[0].Value)
	var got struct {
		Decision string                    `json:"decision"`
		Timings  []kernelPrepareDiagnostic `json:"kernel_preparation"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Decision != "existing-model-boundary" || len(got.Timings) != 1 || got.Timings[0].OperationID != "operation" || got.Timings[0].RunnerAttempt != 7 {
		t.Fatalf("audit lost original binding: %s err=%v", raw, err)
	}
	closeKernelPrepareDiagnostics(run)
	if entries, _ := store.List(sessionRunnerCommunicationAuditNamespace); len(entries) != 1 {
		t.Fatal("empty closing buffer caused another write")
	}
}

func TestKernelPrepareAuditOverflowAndClosedBufferAreExplicitAndBounded(t *testing.T) {
	var buffer kernelPrepareDiagnosticBuffer
	for range maxKernelPrepareDiagnostics + 3 {
		buffer.append(kernelPrepareDiagnostic{Version: 1})
	}
	records, dropped := buffer.take()
	if len(records) != maxKernelPrepareDiagnostics || dropped != 3 {
		t.Fatalf("unbounded or hidden overflow: count=%d dropped=%d", len(records), dropped)
	}
	buffer.append(kernelPrepareDiagnostic{OperationID: "unpublished"})
	loss := buffer.close()
	if !loss.Unavailable || loss.Dropped != 1 {
		t.Fatalf("closing claimed a durable diagnostic: %+v", loss)
	}
	buffer.append(kernelPrepareDiagnostic{OperationID: "late"})
	records, dropped = buffer.take()
	if len(records) != 0 || dropped != 0 || buffer.close() != loss {
		t.Fatalf("late record mutated a closed diagnostic: %v %d", records, dropped)
	}
}

func TestKernelPrepareAuditClosingDoesNotWriteWhileStoreMutexBusy(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	srv := &Server{runtimeStore: store}
	run := &sessionRunnerChatRun{SessionID: "session", Attempt: 3}
	if _, err := store.Set("fixture", "existing", true); err != nil {
		t.Fatal(err)
	}
	srv.recordSessionRunnerCommunicationAudit(context.Background(), run, map[string]any{"decision": "existing-boundary"})
	locked, release, edited := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		edited <- store.EditNamespace("fixture", func(map[string]runtimekv.Entry) (bool, error) { close(locked); <-release; return false, nil })
	}()
	<-locked
	defer close(release)
	run.kernelPrepareDiagnostics.append(kernelPrepareDiagnostic{Version: 1, OperationID: "operation", Attempt: 3})
	finished := make(chan struct{})
	go func() { closeKernelPrepareDiagnostics(run); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("diagnostic blocked on the execution-independent store lock")
	}
	if loss := run.kernelPrepareDiagnostics.close(); !loss.Unavailable || loss.Dropped != 1 {
		t.Fatalf("closing did not declare the unpublished diagnostic unavailable: %+v", loss)
	}
}

func TestKernelPrepareAuditClosingNeverInitializesAnUnusedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-created.sqlite")
	store := runtimekv.New(path)
	t.Cleanup(func() { _ = store.Close() })
	run := &sessionRunnerChatRun{SessionID: "legacy", Attempt: 1}
	run.kernelPrepareDiagnostics.append(kernelPrepareDiagnostic{Version: 1, OperationID: "operation", Attempt: 1})
	closeKernelPrepareDiagnostics(run)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("optional closing diagnostic initialized the database: %v", err)
	}
}

func TestKernelPrepareAuditLateAttemptIsDroppedRatherThanRelabelled(t *testing.T) {
	var buffer kernelPrepareDiagnosticBuffer
	buffer.append(kernelPrepareDiagnostic{Attempt: 3, OperationID: "old-operation"})
	records, dropped := buffer.takeForAttempt(4)
	if len(records) != 0 || dropped != 1 {
		t.Fatalf("old operation was attributed to the new attempt: %+v %d", records, dropped)
	}
}

func TestKernelPrepareAuditClosingDoesNotWriteWhileSQLiteWriterHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	store := runtimekv.New(path)
	t.Cleanup(func() { _ = store.Close() })
	srv := &Server{runtimeStore: store}
	run := &sessionRunnerChatRun{SessionID: "session", Attempt: 3}
	srv.recordSessionRunnerCommunicationAudit(context.Background(), run, map[string]any{"decision": "original-boundary"})
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	tx, err := locker.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE runtime_state_entries SET version=version WHERE namespace=?", sessionRunnerCommunicationAuditNamespace); err != nil {
		t.Fatal(err)
	}
	run.kernelPrepareDiagnostics.append(kernelPrepareDiagnostic{Version: 1, OperationID: "operation", Attempt: 3})
	done := make(chan struct{})
	go func() { closeKernelPrepareDiagnostics(run); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SQLite busy wait delayed diagnostic-only run closing")
	}
	if loss := run.kernelPrepareDiagnostics.close(); !loss.Unavailable || loss.Dropped != 1 {
		t.Fatalf("pending diagnostic was silently treated as durable: %+v", loss)
	}
	// The writer transaction remains held until deferred rollback. WAL reads
	// can still prove that closing did not add another audit row.
	entries, err := store.List(sessionRunnerCommunicationAuditNamespace)
	if err != nil || len(entries) != 1 {
		t.Fatalf("closing added an audit write: %d err=%v", len(entries), err)
	}
}
