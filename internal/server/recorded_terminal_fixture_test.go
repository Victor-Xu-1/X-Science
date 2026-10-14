package server

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const recordedTerminalMessage = "The original request completed."

type recordedTerminalFixture struct {
	ctx       context.Context
	store     *workspace.Store
	repo      *transcriptstore.Repository
	db        *sql.DB
	server    *Server
	stream    transcriptstore.Stream
	authority *transcriptRunnerAuthority
	dispatch  workspace.CompatibilityFrameResumeDispatch
	finish    transcriptstore.Event
	status    string
}

func newRecordedTerminalFixture(t *testing.T) *recordedTerminalFixture {
	t.Helper()
	return newRecordedTerminalFixtureWithStatus(t, "completed")
}

func newRecordedTerminalFixtureWithStatus(t *testing.T, status string) *recordedTerminalFixture {
	t.Helper()
	return newRecordedTerminalFixtureWithLease(t, status, time.Minute)
}

func newRecordedTerminalFixtureWithLease(t *testing.T, status string, dispatchTTL time.Duration) *recordedTerminalFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "recorded-project", "recorded-frame")
	srv := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = srv.Close(closeCtx)
	})
	if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "recorded-frame", MessageUUID: "recorded-input", ClientMessageID: "recorded-input", Text: "Review the original evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "recorded-frame")
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	initial, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "initial-recorded-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !initial.Claimed {
		t.Fatalf("initial claim=%+v err=%v", initial, err)
	}
	if _, _, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: initial.Claim, ClientMessageID: "recorded-checkpoint", Phase: transcriptstore.RunnerPhasePlanning,
		Resumable: true, PayloadJSON: []byte(`{"status":"running"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: initial.Claim, ClientMessageID: "recorded-failed", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("recorded-worker", dispatchTTL)
	if err != nil || !found {
		t.Fatalf("dispatch found=%t err=%v", found, err)
	}
	options := SessionRunnerChatOptions{SessionID: stream.FrameID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID)}
	if err := srv.configureTranscriptFrameResume(ctx, dispatch, &options); err != nil {
		t.Fatal(err)
	}
	authority, claimed, authoritative, err := srv.claimTranscriptFrameRunner(ctx, stream.FrameID, options.RunnerID,
		time.Minute, options.TranscriptResumeSource, options.TranscriptCheckpoint)
	if err != nil || !claimed || !authoritative || authority == nil {
		t.Fatalf("resume claimed=%t authoritative=%t err=%v", claimed, authoritative, err)
	}
	finish, err := srv.finishTranscriptRunner(ctx, authority, status, recordedTerminalMessage)
	if err != nil || finish.EventID <= 0 {
		t.Fatalf("finish=%+v err=%v", finish, err)
	}
	return &recordedTerminalFixture{ctx, store, repo, db, srv, stream, authority, dispatch, finish, status}
}

func (f *recordedTerminalFixture) completeInput() workspace.CompleteCompatibilityFrameResumeDispatchInput {
	return workspace.CompleteCompatibilityFrameResumeDispatchInput{
		ResumeEventID: f.dispatch.ResumeEvent.ID, ExpectedAttempt: f.dispatch.Attempt, ClaimToken: f.dispatch.ClaimToken,
		Status: f.status, Message: recordedTerminalMessage,
		RunnerTerminal: &transcriptstore.RunnerTerminalReference{Claim: f.authority.Claim, EventID: f.finish.EventID},
	}
}

func (f *recordedTerminalFixture) appendFollowup(t *testing.T) {
	t.Helper()
	response := compatJSONRequest(t, f.server.Handler(), http.MethodPost, "/api/frames/"+f.stream.FrameID+"/message", "local",
		map[string]any{"input_data": map[string]any{"request": "Review the additional evidence."}}, http.StatusOK)
	if response["status"] != "accepted" {
		t.Fatalf("follow-up response=%+v", response)
	}
}

func (f *recordedTerminalFixture) presentation(t *testing.T) [3]string {
	t.Helper()
	var snapshot [3]string
	if err := f.db.QueryRow(`SELECT frame.status,COALESCE(metadata.status_description,''),COALESCE(CAST(metadata.completed_at AS TEXT),'')
		FROM frames frame LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id WHERE frame.id=?`, f.stream.FrameID).
		Scan(&snapshot[0], &snapshot[1], &snapshot[2]); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
