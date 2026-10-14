package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Exercise the real durable boundaries in their legal order without a model:
// runner finish -> accepted composer input -> the owning dispatch's completion.
func TestResumeDispatchCompletionPreservesAcceptedComposerInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, repo, db := newTranscriptWebFixture(t)
	const frameID = "completion-input-handoff"
	seedTranscriptWebFrame(t, store, "local", "completion-input-project", frameID)
	srv := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = srv.Close(closeCtx)
	})
	if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "original-input", ClientMessageID: "original-input", Text: "Review the supplied evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", frameID)
	if err != nil || !found {
		t.Fatalf("initial stream found=%t err=%v", found, err)
	}
	initial, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "original-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !initial.Claimed {
		t.Fatalf("initial claim=%+v err=%v", initial, err)
	}
	if _, _, _, err := repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{
		Claim: initial.Claim, ClientMessageID: "original-checkpoint", Phase: transcriptstore.RunnerPhasePlanning,
		Resumable: true, PayloadJSON: []byte(`{"status":"running"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{
		Claim: initial.Claim, ClientMessageID: "original-failed", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResumeCompatibilityFrameConversation(frameID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("handoff-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("dispatch found=%t err=%v", found, err)
	}
	options := SessionRunnerChatOptions{SessionID: frameID, RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID)}
	if err := srv.configureTranscriptFrameResume(ctx, dispatch, &options); err != nil {
		t.Fatal(err)
	}
	authority, claimed, authoritative, err := srv.claimTranscriptFrameRunner(ctx, frameID, options.RunnerID,
		time.Minute, options.TranscriptResumeSource, options.TranscriptCheckpoint)
	if err != nil || !claimed || !authoritative || authority == nil {
		t.Fatalf("resume claim claimed=%t authoritative=%t err=%v", claimed, authoritative, err)
	}
	finish, err := srv.finishTranscriptRunner(ctx, authority, "completed", "The original review is complete.")
	if err != nil || finish.EventID <= 0 {
		t.Fatalf("runner finish=%+v err=%v", finish, err)
	}
	frame, found, err := store.GetFrame(frameID)
	if err != nil || !found || frame.Status != "completed" {
		t.Fatalf("runner did not establish completed boundary: frame=%+v found=%t err=%v", frame, found, err)
	}

	const followup = "Review the additional evidence as a separate follow-up."
	response := compatJSONRequest(t, srv.Handler(), http.MethodPost, "/api/frames/"+frameID+"/message", "local", map[string]any{
		"input_data": map[string]any{"request": followup}, "client_mutation_id": "completion-followup",
	}, http.StatusOK)
	messageID := stringValue(response["message_id"])
	if response["status"] != "accepted" || messageID == "" {
		t.Fatalf("composer response=%+v", response)
	}
	pending, found, err := repo.GetFramePendingInputState(ctx, "local", frameID)
	if err != nil || !found || pending.InputRevision <= authority.Claim.ClaimedInputRevision ||
		pending.ConsumedInputRevision != authority.Claim.ClaimedInputRevision {
		t.Fatalf("composer was not independently admitted: pending=%+v found=%t err=%v", pending, found, err)
	}
	frame, found, err = store.GetFrame(frameID)
	if err != nil || !found || frame.Status != "processing" {
		t.Fatalf("accepted composer did not reactivate frame: frame=%+v found=%t err=%v", frame, found, err)
	}
	if queued, err := store.CompatibilityQueuedFrameIDs(); err != nil || len(queued) != 0 {
		t.Fatalf("accepted composer unexpectedly queued: frames=%v err=%v", queued, err)
	}
	if _, repeated, err := store.CompleteCompatibilityFrameResumeDispatch(workspace.CompleteCompatibilityFrameResumeDispatchInput{
		ResumeEventID: dispatch.ResumeEvent.ID, ExpectedAttempt: dispatch.Attempt, ClaimToken: dispatch.ClaimToken,
		Status: "completed", Message: "The original review is complete.",
		RunnerTerminal: &transcriptstore.RunnerTerminalReference{Claim: authority.Claim, EventID: finish.EventID},
		Details:        map[string]any{"runnerId": authority.Claim.RunnerID, "runnerAttempt": authority.Claim.Attempt, "finishEventId": finish.EventID},
	}); err != nil || repeated {
		t.Fatalf("owned dispatch completion repeated=%t err=%v", repeated, err)
	}
	var canonicalInputs, receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'
		AND client_message_id=? AND json_extract(payload_json,'$.text')=? AND json_extract(payload_json,'$.messageUuid')=?`,
		stream.UID, messageID, followup, messageID).Scan(&canonicalInputs); err != nil || canonicalInputs != 1 {
		t.Fatalf("new canonical input count=%d err=%v", canonicalInputs, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=? AND attempt=? AND event_id=?`,
		stream.UID, authority.Claim.Attempt, finish.EventID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("original finish receipt count=%d err=%v", receipts, err)
	}
	frame, found, err = store.GetFrame(frameID)
	if err != nil || !found || frame.Status != "processing" {
		t.Errorf("old dispatch terminalized newer accepted input: frame=%+v found=%t err=%v", frame, found, err)
	}
	next, err := repo.ClaimNextRunner(ctx, transcriptstore.ClaimNextRunnerInput{RunnerID: "followup-runner", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != pending.InputRevision {
		t.Errorf("new accepted input is not schedulable: claimed=%t revision=%d want=%d err=%v",
			next.Claimed, next.Claim.ClaimedInputRevision, pending.InputRevision, err)
	}
}
