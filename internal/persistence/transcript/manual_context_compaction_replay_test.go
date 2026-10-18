package transcript_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	transcript "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualContextCompactionSQLCropRetainsBoundaryAndOriginalUser(t *testing.T) {
	ctx := context.Background()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err = store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Compaction", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(ctx, transcript.CreateStreamInput{UID: "frame:frame", OwnerID: "owner", ExternalID: frame.ID,
		SessionID: frame.ID, Kind: transcript.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	user, _, created, err := repo.AppendFrameUserEvent(ctx, transcript.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ClientMessageID: "original-constraints", FrameEventID: "original-event", MessageUUID: "original-message", Text: "Preserve original constraints."})
	if err != nil || !created {
		t.Fatalf("input created=%t err=%v", created, err)
	}
	claim, err := repo.ClaimRunner(ctx, transcript.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "ordinary", TTL: time.Minute, ResumeSource: transcript.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	old, _, err := repo.AppendRunnerEvent(ctx, transcript.AppendEventInput{Claim: claim.Claim, ClientMessageID: "old-assistant", Type: "assistant_message",
		Source: transcript.EventSourcePayload, PayloadJSON: []byte(`{"text":"OLD_ASSISTANT_DETAIL"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = repo.FinishRunner(ctx, transcript.FinishRunnerInput{Claim: claim.Claim, ClientMessageID: "ordinary-finish", Status: "completed",
		PayloadJSON: []byte(`{"status":"completed"}`)}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.ContextCompactionSnapshot(ctx, stream.OwnerID, frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := transcript.ContextCompactionRequest{Schema: transcript.ContextCompactionRequestSchema,
		RequestID: "c9476bfa-0901-4ba1-abf7-07f2ce9314d5", Expected: snapshot.Target}
	if _, err = store.AdmitContextCompaction(ctx, stream.OwnerID, frame.ID, request); err != nil {
		t.Fatal(err)
	}
	claim, err = repo.ClaimRunner(ctx, transcript.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "maintenance", TTL: time.Minute, ResumeSource: transcript.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("maintenance claim=%#v err=%v", claim, err)
	}
	boundary, err := repo.FinishContextCompaction(ctx, claim.Claim, request, map[string]any{
		"status": "completed", "purpose": transcript.ContextCompactionPurpose, "toolPhase": transcript.ContextCompactionPhase,
		"maintenance": request, "summary": "Preserved verified evidence.",
		"compaction": map[string]any{"schema": transcript.ContextCompactionBoundarySchema, "reason": "manual", "request_id": request.RequestID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = repo.AppendFrameUserEvent(ctx, transcript.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ClientMessageID: "next-user", FrameEventID: "next-event", MessageUUID: "next-message", Text: "Continue with the next observation."}); err != nil {
		t.Fatal(err)
	}
	claim, err = repo.ClaimRunner(ctx, transcript.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "next", TTL: time.Minute, ResumeSource: transcript.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("next claim=%#v err=%v", claim, err)
	}
	for _, id := range []string{"later-audit-1", "later-audit-2"} {
		if _, _, _, err = repo.AppendRunnerCheckpoint(ctx, transcript.AppendRunnerCheckpointInput{Claim: claim.Claim, ClientMessageID: id,
			Phase: transcript.RunnerPhaseExecuting, PayloadJSON: []byte(`{"status":"running"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := repo.ListRunnerReplay(ctx, transcript.ListRunnerReplayInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, MessageLimit: 100, CheckpointLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	contains := func(id int64) bool {
		for _, event := range events {
			if event.Event.EventID == id {
				return true
			}
		}
		return false
	}
	if contains(old.EventID) {
		t.Error("SQL replay did not crop the pre-boundary assistant message")
	}
	if !contains(boundary.BoundaryEventID) {
		t.Error("SQL replay lost the manual boundary beyond recent checkpoint limit")
	}
	if !contains(user.EventID) {
		t.Error("SQL crop removed original user constraints")
	}
	for _, event := range events {
		if event.Event.EventID == boundary.BoundaryEventID && (event.CompactionBoundary == nil || !event.CompactionBoundary.Matches(boundary.BoundaryEventID)) {
			t.Error("SQL-selected boundary did not carry canonical in-memory proof")
		}
	}
}
