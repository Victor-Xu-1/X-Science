package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

// This is a controlled reader-wiring fixture, not an upgrade of user data.
// The real v73->v74 migration/source-membership fence is tested by workspace.
func freezeNamespaceReaderFixture(t *testing.T, db *sql.DB, event transcriptstore.Event) {
	t.Helper()
	digest, err := transcriptstore.NamespaceHistoricalEventDigest(event)
	if err != nil {
		t.Fatal(err)
	}
	var guard string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='namespace_transcript_event_receipts_insert_guard'`).Scan(&guard); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []string{`DROP TRIGGER namespace_transcript_event_receipts_insert_guard`, guard} {
		if statement == guard {
			if _, err := tx.Exec(`INSERT INTO namespace_transcript_event_receipts(stream_uid,event_id,source_sha256) VALUES(?,?,?)`, event.StreamUID, event.EventID, digest); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertNamespaceNativeFixture(t *testing.T, db *sql.DB, event transcriptstore.Event) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO transcript_events(stream_uid,event_id,publication_seq,client_message_id,event_type,source,payload_json,created_at) VALUES(?,?,?,?,?,'payload',?,?)`, event.StreamUID, event.EventID, event.PublicationSeq, event.ClientMessageID, event.Type, event.PayloadJSON, event.CreatedAt); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceRunnerReadersRequireExactFrozenNativeSource(t *testing.T) {
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "namespace-owner", "namespace-project", "namespace-frame")
	ctx := context.Background()
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{UID: "namespace-stream", OwnerID: "namespace-owner", ExternalID: "namespace-frame", SessionID: "namespace-frame", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "namespace-project", RootFrameID: "namespace-frame", FrameID: "namespace-frame", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceStore: store, transcriptStore: repo}
	event := transcriptstore.Event{StreamUID: stream.UID, EventID: 100, PublicationSeq: 100, ClientMessageID: "namespace-cause", Type: "runner_checkpoint", Source: transcriptstore.EventSourcePayload, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), PayloadJSON: []byte(`{"type":"runner_checkpoint","interruption_cause":{"schema":"synon.runner_interruption_cause.v1","reason_code":"transient_provider_error","detail":"original retained cause"}}`)}
	insertNamespaceNativeFixture(t, db, event)
	var message eventjournal.Message
	if err := json.Unmarshal(event.PayloadJSON, &message); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.parseRunnerNamespaceCause(ctx, stream.OwnerID, event, message); err == nil {
		t.Fatal("late/unproven old schema admitted")
	}
	freezeNamespaceReaderFixture(t, db, event)
	before := append([]byte(nil), event.PayloadJSON...)
	// A valid source witness must not authorize a different caller payload.
	forged := eventjournal.Message{transcriptstore.RunnerInterruptionCauseField: map[string]any{"schema": "synon.runner_interruption_cause.v1", "reason_code": "transient_provider_error", "detail": "forged detail"}}
	cause, present, err := server.parseRunnerNamespaceCause(ctx, stream.OwnerID, event, forged)
	if err != nil || !present || cause.Detail != "original retained cause" {
		t.Fatalf("cause=%#v present=%v err=%v", cause, present, err)
	}
	if !bytes.Equal(before, event.PayloadJSON) {
		t.Fatal("original source bytes mutated")
	}
	if _, _, err := server.parseRunnerNamespaceCause(ctx, "foreign-owner", event, message); err == nil {
		t.Fatal("foreign owner read historical contract")
	}
	changed := event
	changed.PayloadJSON = bytes.Replace(event.PayloadJSON, []byte("original retained cause"), []byte("changed retained cause"), 1)
	var changedMessage eventjournal.Message
	_ = json.Unmarshal(changed.PayloadJSON, &changedMessage)
	if _, _, err := server.parseRunnerNamespaceCause(ctx, stream.OwnerID, changed, changedMessage); err == nil {
		t.Fatal("changed native bytes reused original source proof")
	}
}

func TestNamespaceAskUserProjectionCanonicalizesOnlyCopiedFrozenMetadata(t *testing.T) {
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "ask-namespace-owner", "ask-namespace-project", "ask-namespace-frame")
	ctx := context.Background()
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{UID: "ask-namespace-stream", OwnerID: "ask-namespace-owner", ExternalID: "ask-namespace-frame", SessionID: "ask-namespace-frame", Kind: transcriptstore.StreamKindFrameRef, ProjectID: "ask-namespace-project", RootFrameID: "ask-namespace-frame", FrameID: "ask-namespace-frame", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := repo.GetBranchState(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	prompt := transcriptstore.AskUserPromptV1{Version: 1, Origin: transcriptstore.AskUserOriginV1{Version: 1, StreamUID: stream.UID, Epoch: 1, FrameID: stream.FrameID, BranchID: branch.ActiveBranchID, BranchGeneration: branch.Generation, RunnerAttempt: 1, ToolUseID: "ask-call", ToolUseFrameEventID: "tool-event", PendingFrameEventID: "pending-event", PromptClientMessageID: "prompt-client", PendingClientMessageID: "pending-client"}, ToolUseID: "ask-call", ToolName: "ask_user", Questions: []transcriptstore.AskUserQuestionV1{{Question: "Choose the next route", Header: "Next", Options: []transcriptstore.AskUserQuestionOptionV1{{Label: "Continue"}, {Label: "Revise"}}, StageProgress: &transcriptstore.AskUserStageProgressV1{Schema: "synon.plan_stage_progress.v1", PlanArtifactID: "plan", PlanVersionID: "version", CompletedCount: 1, RemainingCount: 1, CompletedSteps: []transcriptstore.AskUserStageStepV1{{ID: "a", Title: "Inspect", Status: "completed"}}, RemainingSteps: []transcriptstore.AskUserStageStepV1{{ID: "b", Title: "Run", Status: "blocked"}}}}}}
	raw, err := json.Marshal(prompt)
	if err != nil {
		t.Fatal(err)
	}
	event := transcriptstore.Event{StreamUID: stream.UID, EventID: 100, PublicationSeq: 100, ClientMessageID: "prompt-client", Type: transcriptstore.AskUserPromptEventType, Source: transcriptstore.EventSourcePayload, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), PayloadJSON: raw}
	insertNamespaceNativeFixture(t, db, event)
	projected := transcriptstore.ProjectedEvent{Event: event, ResolvedPayloadJSON: raw}
	server := &Server{workspaceStore: store, transcriptStore: repo}
	if _, err := server.normalizeNamespaceAskUserProjection(ctx, stream.OwnerID, projected); err == nil {
		t.Fatal("unproven historical prompt admitted")
	}
	freezeNamespaceReaderFixture(t, db, event)
	normalized, err := server.normalizeNamespaceAskUserProjection(ctx, stream.OwnerID, projected)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := transcriptstore.DecodeAskUserPromptV1(normalized.ResolvedPayloadJSON)
	if err != nil || decoded.Questions[0].StageProgress.Schema != "x-science.plan_stage_progress.v1" {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if !bytes.Equal(raw, normalized.Event.PayloadJSON) || !bytes.Equal(raw, projected.ResolvedPayloadJSON) {
		t.Fatal("source or caller bytes rewritten")
	}
	if _, err := server.normalizeNamespaceAskUserProjection(ctx, "foreign", projected); err == nil {
		t.Fatal("foreign historical prompt admitted")
	}
}
