package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	eventjournal "x-science/internal/persistence/journal"
)

func TestWorkspaceMemoryCommitRollsBackCursorFailure(t *testing.T) {
	server := openWorkspaceMemoryToolServer(t)
	server.memoryConfig.ExtractMode = "haiku"
	server.memoryConfig.PIClassifierEnabled = false
	ctx := t.Context()
	if _, err := server.workspaceStore.SetLastExtractMessageIndex(ctx, "frame-root", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := server.workspaceStore.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_extraction_cursor
		 BEFORE UPDATE OF last_extract_msg_idx ON frame_runtime_metadata
		 WHEN NEW.last_extract_msg_idx > OLD.last_extract_msg_idx
		 BEGIN SELECT RAISE(ABORT, 'injected extraction cursor failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	journal := eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	server.eventJournal = journal
	for _, message := range []eventjournal.Message{
		{"type": "message", "role": "user", "text": "Remember that reproducible reports must record explicit random seeds."},
		{"type": "message", "role": "assistant", "text": "I will preserve that durable reporting preference."},
		{"type": "runner_finished", "status": "completed", "runnerAttempt": 1},
	} {
		if _, err := journal.Append("frame-root", message, eventjournal.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	runtime := newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			return map[string]any{"append": []any{map[string]any{
				"text":     "Reproducible reports must record explicit random seeds.",
				"evidence": "stated", "entity": "project:project-1",
			}}}, nil
		})
	})
	result, runErr := runtime.RunCompletedRoot(ctx, "frame-root", 3)
	rows, err := server.workspaceStore.ListMemoryExtractionRows(ctx, "user-1", "project-1")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := server.workspaceStore.LastExtractMessageIndex(ctx, "frame-root")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 || cursor != 0 {
		t.Fatalf("cursor abort leaked effects: facts=%d cursor=%d gate=%q run_error=%v cursor_error=%q", len(rows), cursor, result.Gate, runErr, result.CursorError)
	}
	if runErr == nil && result.Gate == "done" {
		t.Fatalf("cursor abort reported successful completion: %#v", result)
	}
}
