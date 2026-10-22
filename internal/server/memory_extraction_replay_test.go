package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	eventjournal "x-science/internal/persistence/journal"
)

func newAtomicExtractionRuntime(t *testing.T) (*Server, *memoryExtractionRuntime, *int) {
	t.Helper()
	server := openWorkspaceMemoryToolServer(t)
	server.memoryConfig.ExtractMode = "haiku"
	server.memoryConfig.PIClassifierEnabled = false
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	for _, message := range []eventjournal.Message{
		{"type": "message", "role": "user", "text": "Remember that reproducible reports record explicit random seeds."},
		{"type": "message", "role": "assistant", "text": "That durable reporting preference is confirmed."},
		{"type": "runner_finished", "status": "completed", "runnerAttempt": 1},
	} {
		if _, err := server.eventJournal.Append("frame-root", message, eventjournal.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	calls := new(int)
	runtime := newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			*calls++
			return map[string]any{"append": []any{map[string]any{
				"text": "Reproducible reports record explicit random seeds.", "evidence": "stated", "entity": "project:project-1",
			}}}, nil
		})
	})
	return server, runtime, calls
}

func TestWorkspaceMemoryRuntimeReplaysWithoutVolatileForkedRequest(t *testing.T) {
	server, runtime, calls := newAtomicExtractionRuntime(t)
	ctx := t.Context()
	original, err := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	if err != nil || original.Gate != "done" || len(original.Applied.Appended) != 1 {
		t.Fatalf("initial extraction=%#v err=%v", original, err)
	}
	server.memoryConfig.ExtractMode = "forked"
	if err := server.workspaceStore.SetMemoryEnabled(ctx, "user-1", false); err != nil {
		t.Fatal(err)
	}
	if err := server.setMemoryAutoExtractionEnabled("user-1", false); err != nil {
		t.Fatal(err)
	}
	// A new runtime has no remembered request/cadence/session event registry.
	runtime = newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		t.Error("committed replay constructed an extractor")
		return nil
	})
	replayed, err := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	if err != nil || !replayed.Replayed || replayed.Gate != original.Gate ||
		replayed.CursorBefore != original.CursorBefore || replayed.CursorAfter != original.CursorAfter ||
		!reflect.DeepEqual(replayed.Applied, original.Applied) || *calls != 1 {
		t.Fatalf("runtime replay=%#v original=%#v calls=%d err=%v", replayed, original, *calls, err)
	}
}

func TestWorkspaceMemoryCursorAbortCanRetryAndReplay(t *testing.T) {
	server, runtime, calls := newAtomicExtractionRuntime(t)
	ctx := t.Context()
	if _, err := server.workspaceStore.SetLastExtractMessageIndex(ctx, "frame-root", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := server.workspaceStore.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_retry_cursor BEFORE UPDATE OF last_extract_msg_idx ON frame_runtime_metadata
		 WHEN NEW.last_extract_msg_idx > OLD.last_extract_msg_idx BEGIN SELECT RAISE(ABORT,'retry cursor failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	if err == nil || failed.Gate == "done" || len(failed.Applied.Appended) != 0 {
		t.Fatalf("aborted extraction=%#v err=%v", failed, err)
	}
	if err := server.workspaceStore.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TRIGGER fail_retry_cursor`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	committed, err := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	if err != nil || committed.Gate != "done" || committed.Replayed || len(committed.Applied.Appended) != 1 || *calls != 2 {
		t.Fatalf("retry extraction=%#v calls=%d err=%v", committed, *calls, err)
	}
	replayed, err := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	rows, rowsErr := server.workspaceStore.ListMemoryExtractionRows(ctx, "user-1", "project-1")
	if err != nil || rowsErr != nil || !replayed.Replayed || *calls != 2 || len(rows) != 1 ||
		!reflect.DeepEqual(replayed.Applied, committed.Applied) {
		t.Fatalf("retry replay=%#v calls=%d rows=%d err=%v rowsErr=%v", replayed, *calls, len(rows), err, rowsErr)
	}
}

func TestWorkspaceMemoryDelayedLegacyCompletionCannotRewindNewerCursor(t *testing.T) {
	server, runtime, _ := newAtomicExtractionRuntime(t)
	ctx := t.Context()
	older, err := runtime.captureCompletedMemorySource(ctx, "frame-root", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []eventjournal.Message{
		{"type": "message", "role": "user", "text": "Also preserve confidence intervals in these reproducible reports."},
		{"type": "message", "role": "assistant", "text": "The newer durable reporting convention is confirmed."},
		{"type": "runner_finished", "status": "completed", "runnerAttempt": 2},
	} {
		if _, err := server.eventJournal.Append("frame-root", message, eventjournal.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	newer, err := runtime.RunCompletedRoot(ctx, "frame-root", 6)
	if err != nil || newer.Gate != "done" || newer.CursorAfter < 3 {
		t.Fatalf("newer extraction=%#v err=%v", newer, err)
	}
	stale, staleErr := runtime.runCompletedRoot(ctx, "frame-root", older)
	cursor, err := server.workspaceStore.LastExtractMessageIndex(ctx, "frame-root")
	rows, rowsErr := server.workspaceStore.ListMemoryExtractionRows(ctx, "user-1", "project-1")
	if err != nil || rowsErr != nil || cursor != newer.CursorAfter || len(rows) != 1 || stale.Gate == "done" {
		t.Fatalf("delayed completion rewound newer work: newer=%#v stale=%#v cursor=%d rows=%d staleErr=%v err=%v rowsErr=%v", newer, stale, cursor, len(rows), staleErr, err, rowsErr)
	}
}
