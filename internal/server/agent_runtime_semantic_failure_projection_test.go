package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

type semanticFailureProjectionFixture struct {
	gateway serverAgentRuntimeToolGateway
	repo    *transcriptstore.Repository
	db      *sql.DB
	stream  transcriptstore.Stream
	claim   transcriptstore.RunnerClaim
}

func newSemanticFailureProjectionFixture(t *testing.T) semanticFailureProjectionFixture {
	t.Helper()
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "payload-budget-project", "payload-budget-frame")
	ctx := context.Background()
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "payload-budget-stream", OwnerID: "local", ExternalID: "payload-budget-frame", SessionID: "payload-budget-frame",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "payload-budget-project", RootFrameID: "payload-budget-frame", FrameID: "payload-budget-frame", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "payload-user", FrameEventID: "payload-user-event",
		MessageUUID: "payload-user-message", Text: "Process a synthetic input",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "payload-budget-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim: claimed=%t err=%v", claimed.Claimed, err)
	}
	return semanticFailureProjectionFixture{gateway: serverAgentRuntimeToolGateway{
		server: &Server{workspaceStore: store, transcriptStore: repo}, sessionID: stream.SessionID,
	}, repo: repo, db: db, stream: stream, claim: claimed.Claim}
}

func (f semanticFailureProjectionFixture) checkpoint(t *testing.T, id string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: id, Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func semanticProjectionFailure(input map[string]any) map[string]any {
	return map[string]any{
		"toolName": "python", "toolInput": input, "toolCapabilities": []string{"runtime-execution"},
		"toolPhase": "failed", "status": "failed",
		"toolResult": map[string]any{"ok": false, "executed": true, "code": "python_execution_failed", "error": "synthetic execution failure"},
	}
}

func TestSemanticFailureProjectionDoesNotDependOnUnusedArtifacts(t *testing.T) {
	f := newSemanticFailureProjectionFixture(t)
	ctx := context.Background()
	input := map[string]any{"environment": "analysis", "code": "synthetic_job()"}
	f.checkpoint(t, "failed", semanticProjectionFailure(input))
	want, err := f.gateway.durableSemanticFailureBoundary(ctx, "python", input)
	if err != nil || want == nil {
		t.Fatalf("baseline failure receipt was not retained: boundary=%v err=%v", want, err)
	}
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	// Only this owned fixture's artifact-association table becomes unreadable.
	// Its canonical event/payload tables and authority remain available.
	if _, err := f.db.Exec(`ALTER TABLE transcript_artifact_refs RENAME TO fixture_artifact_refs`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.Exec(`ALTER TABLE fixture_artifact_refs RENAME TO transcript_artifact_refs`); err != nil {
			t.Error(err)
		}
	})
	query := transcriptstore.ListProjectedEventsInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 1000}
	if _, err := f.repo.ListProjectedEvents(ctx, query); err == nil || !strings.Contains(err.Error(), "transcript_artifact_refs") {
		t.Fatalf("full reader did not prove the artifact-only fault: %v", err)
	}
	if page, err := f.repo.ListProjectedCoordinateEvents(ctx, query); err != nil || len(page) == 0 {
		t.Fatalf("event/payload reader was unavailable too: events=%d err=%v", len(page), err)
	}
	got, err := f.gateway.durableSemanticFailureBoundary(ctx, "python", input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("unused artifact associations blocked or changed the payload ledger: boundary=%v err=%v", got, err)
	}
}

func TestSemanticFailurePayloadProjectionKeepsRepairTurnAndMissingPayload(t *testing.T) {
	f := newSemanticFailureProjectionFixture(t)
	ctx := context.Background()
	input := map[string]any{"environment": "analysis", "code": "synthetic_job()"}
	assertBlocked := func(want bool) {
		t.Helper()
		boundary, err := f.gateway.durableSemanticFailureBoundary(ctx, "python", input)
		if err != nil || (boundary != nil) != want {
			t.Fatalf("blocked=%t want=%t err=%v", boundary != nil, want, err)
		}
	}
	f.checkpoint(t, "failed-before-repair", semanticProjectionFailure(input))
	assertBlocked(true)
	f.checkpoint(t, "committed-repair", map[string]any{
		"toolName": "edit_file", "toolCapabilities": []string{"artifact-write"}, "toolPhase": "completed", "status": "completed",
		"toolResult": map[string]any{"ok": true, "executed": true, "changed": true},
	})
	assertBlocked(false)
	f.checkpoint(t, "failed-before-next-turn", semanticProjectionFailure(input))
	assertBlocked(true)
	next, _, _, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "next-user", FrameEventID: "next-user-event",
		MessageUUID: "next-user-message", Text: "Start the next synthetic input",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked(false)
	// Model a legacy reference whose canonical payload is missing. Keep its
	// source/NULL columns schema-valid; do not disable any database constraints.
	// This owned fault must still fail closed, unlike unused artifact metadata.
	changed, err := f.db.Exec(`UPDATE transcript_events SET source='frame_ref',payload_json=NULL,
		frame_event_id='missing-payload-fixture' WHERE stream_uid=? AND event_id=?`, f.stream.UID, next.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := changed.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("missing-payload fixture did not change exactly one event: count=%d err=%v", count, err)
	}
	boundary, err := f.gateway.durableSemanticFailureBoundary(ctx, "python", input)
	if !errors.Is(err, transcriptstore.ErrEventConflict) || boundary != nil {
		t.Fatalf("missing canonical payload was treated as empty/successful history: boundary=%v err=%v", boundary, err)
	}
}
