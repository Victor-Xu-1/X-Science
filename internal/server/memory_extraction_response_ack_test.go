package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	workspace "x-science/internal/persistence/workspace"
)

func TestMemoryExtractionRejectsUnacknowledgedProviderResponseBeforeCommit(t *testing.T) {
	for _, mode := range []string{"forked", "compact"} {
		t.Run(mode, func(t *testing.T) { testMemoryExtractionResponseAcknowledgement(t, mode) })
	}
}

func testMemoryExtractionResponseAcknowledgement(t *testing.T, mode string) {
	t.Helper()
	const frameID, sourceView = "frame-root", "ORIGINAL_COMPLETED_SOURCE_VIEW"
	const privateBody = "UNACKNOWLEDGED_PROVIDER_BODY_CANARY"
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || r.URL.Path != "/v1/messages" || payload["model"] != memoryExtractionTestModel {
			t.Errorf("fixture did not reach the selected real provider contract: path=%q model=%v err=%v", r.URL.Path, payload["model"], err)
			http.Error(w, "invalid local provider fixture request", http.StatusBadRequest)
			return
		}
		if mode == "forked" {
			encoded, _ := json.Marshal(payload)
			if !strings.Contains(string(encoded), sourceView) {
				t.Error("forked extractor did not consume the original attempt's remembered request")
			}
		} else {
			choice, _ := payload["tool_choice"].(map[string]any)
			if choice["type"] != "tool" || choice["name"] != memoryextract.EmitToolName {
				t.Error("compact extractor did not request the existing emit acknowledgement")
			}
		}
		// First response is valid provider transport but NOT an extraction ack.
		// Second response explicitly acknowledges an empty operation batch.
		content := []any{map[string]any{"type": "text", "text": privateBody}}
		stop := "end_turn"
		if calls.Add(1) > 1 {
			if mode == "forked" {
				content = []any{map[string]any{"type": "text", "text": "{}"}}
			} else {
				content = []any{map[string]any{"type": "tool_use", "id": "toolu_memory_ack", "name": memoryextract.EmitToolName, "input": map[string]any{}}}
				stop = "tool_use"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_memory_ack", "role": "assistant", "model": memoryExtractionTestModel,
			"content": content, "stop_reason": stop,
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 3},
		})
	}))
	defer provider.Close()
	server := openWorkspaceMemoryToolServer(t)
	registerMemoryExtractionTestProvider(t, server, provider.URL)
	server.httpClient = provider.Client()
	server.memoryConfig.ExtractEnabled, server.memoryConfig.ExtractDelta = true, true
	server.memoryConfig.ExtractEveryN, server.memoryConfig.PIClassifierEnabled = 1, false
	server.memoryConfig.ExtractMode = mode
	if mode == "compact" {
		server.memoryConfig.ExtractMode = "haiku" // Existing configuration spelling for compact mode.
	}
	if err := server.sessionStore.Upsert(sessionstore.Session{
		ID: frameID, Project: &sessionstore.Project{ID: "project-1"},
		Orchestration: map[string]any{"frame_id": frameID, "sessionConfig": map[string]any{
			"model": memoryExtractionTestModel, "memory_mode": "on",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.workspaceStore.SetFrameRuntimeMetadata(frameID, workspace.FrameRuntimeMetadata{
		ContextData: map[string]any{"web_assistant": map[string]any{"conversation_overrides": map[string]any{
			"model": memoryExtractionTestModel, "model_revision": float64(1),
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
	for _, message := range []eventjournal.Message{
		{"type": "message", "role": "user", "text": "Reproducible reports must preserve explicit random seeds for future work."},
		{"type": "message", "role": "assistant", "text": "The durable reporting preference is confirmed."},
		{"type": "runner_finished", "status": "completed", "runnerAttempt": 1},
	} {
		if _, err := server.eventJournal.Append(frameID, message, eventjournal.Metadata{}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	runtime := newMemoryExtractionRuntime(server) // Real default compact/forked adapters, never a factory replacement.
	runtime.rememberLastRequest(frameID, 1, agentruntime.ModelRequest{Messages: []agentruntime.Message{
		{Role: "system", Content: sourceView},
		{Role: "user", Content: "Reproducible reports must preserve explicit random seeds for future work."},
	}})
	source, err := runtime.captureCompletedMemorySource(ctx, frameID, 3)
	if err != nil || !source.captured || source.attempt != 1 || source.legacySessionID != frameID || source.request == nil {
		t.Fatalf("the actual completed host source/request was not captured: source=%#v err=%v", source, err)
	}
	commitSource, err := runtime.completedExtractionCommitSource(memoryextract.ApplyScope{
		UserID: "user-1", ProjectID: "project-1", SourceFrameID: frameID,
	}, source)
	if err != nil || commitSource.Legacy == nil || commitSource.Legacy.FinishEventID != 3 {
		t.Fatalf("completed source has no existing durable commit identity: source=%#v err=%v", commitSource, err)
	}
	snapshot, err := runtime.readMemoryExtractionSnapshot(ctx, "user-1", frameID, source)
	if err != nil || snapshot.TerminalStatus != "completed" || len(snapshot.Messages) != 2 {
		t.Fatalf("completed source snapshot is not eligible: snapshot=%#v err=%v", snapshot, err)
	}
	failed, err := runtime.RunCompletedRoot(ctx, frameID, 3)
	if calls.Load() != 1 {
		t.Fatalf("fixture did not make exactly one actual provider request: calls=%d gate=%q err=%v", calls.Load(), failed.Gate, err)
	}
	cursor, cursorErr := server.workspaceStore.LastExtractMessageIndex(ctx, frameID)
	rows, rowsErr := server.workspaceStore.ListMemoryExtractionRows(ctx, "user-1", "project-1")
	_, committedEarly, receiptErr := server.workspaceStore.ReadMemoryExtractionReceipt(ctx, commitSource)
	if err != nil || cursorErr != nil || rowsErr != nil || receiptErr != nil ||
		failed.Gate != "extractor-failed" || failed.ExtractorError == "" || strings.Contains(failed.ExtractorError, privateBody) ||
		failed.CursorAdvanced || cursor != 0 || len(rows) != 0 || committedEarly {
		t.Fatalf("unacknowledged response was not retryable without committed effects: gate=%q diagnostic=%q cursor=%d rows=%d receipt=%t err=%v cursorErr=%v rowsErr=%v receiptErr=%v",
			failed.Gate, failed.ExtractorError, cursor, len(rows), committedEarly, err, cursorErr, rowsErr, receiptErr)
	}
	committed, err := runtime.RunCompletedRoot(ctx, frameID, 3)
	if err != nil || committed.Gate != "done" || committed.Replayed || !committed.CursorAdvanced ||
		committed.CursorBefore != 0 || committed.CursorAfter != len(snapshot.Messages) || calls.Load() != 2 {
		t.Fatalf("same source did not accept exactly one explicit empty acknowledgement: result=%#v calls=%d err=%v", committed, calls.Load(), err)
	}
	saved, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(ctx, commitSource)
	if err != nil || !found || saved.Gate != "done" || saved.InputSHA256 == "" || saved.CursorAfter != committed.CursorAfter {
		t.Fatalf("explicit acknowledgement did not persist its immutable source receipt: saved=%#v found=%t err=%v", saved, found, err)
	}
	// Losing the caller's reply is recovered by a fresh default runtime with no
	// remembered forked request. The committed receipt, not another model, owns it.
	restarted := newMemoryExtractionRuntime(server)
	replayed, err := restarted.RunCompletedRoot(ctx, frameID, 3)
	rows, rowsErr = server.workspaceStore.ListMemoryExtractionRows(ctx, "user-1", "project-1")
	cursor, cursorErr = server.workspaceStore.LastExtractMessageIndex(ctx, frameID)
	if err != nil || rowsErr != nil || cursorErr != nil || !replayed.Replayed || replayed.Gate != "done" ||
		replayed.CursorAfter != committed.CursorAfter || cursor != committed.CursorAfter || len(rows) != 0 ||
		!reflect.DeepEqual(replayed.Applied, committed.Applied) || calls.Load() != 2 {
		t.Fatalf("committed empty acknowledgement was not read-only replayed: replay=%#v calls=%d cursor=%d rows=%d err=%v rowsErr=%v cursorErr=%v",
			replayed, calls.Load(), cursor, len(rows), err, rowsErr, cursorErr)
	}
}
