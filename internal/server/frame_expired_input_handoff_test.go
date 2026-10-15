package server

import (
	"context"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Preserve the actual API phase order while placing the normal runner reclaim
// between its expiry decision and canonical append. No lease/row is rewritten.
func TestFinishRunnerPreservesComposerInputAcceptedAfterLegitimateReclaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, repo, db := newTranscriptWebFixture(t)
	const frameID = "expired-composer-handoff"
	seedTranscriptWebFrame(t, store, "local", "expired-composer-project", frameID)
	srv := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		_ = srv.Close(closeCtx)
	})
	if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "initial-input", ClientMessageID: "initial-input", Text: "Review the initial evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(ctx, "local", frameID)
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	initial, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "initial-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !initial.Claimed {
		t.Fatalf("initial claim=%+v err=%v", initial, err)
	}
	if _, err := repo.InterruptRunner(ctx, transcriptstore.InterruptRunnerInput{
		Claim: initial.Claim, ClientMessageID: "runtime-drain", ReasonCode: "runtime_draining", AutoResume: true,
	}); err != nil {
		t.Fatal(err)
	}
	frame, found, err := store.GetCompatibilityFrame(frameID)
	if err != nil || !found {
		t.Fatalf("frame found=%t err=%v", found, err)
	}
	if direct, err := srv.shouldDirectlyDeliverExpiredTranscriptMessage(frameID); err != nil || !direct {
		t.Fatalf("composer did not select expired direct delivery: direct=%t err=%v", direct, err)
	}
	current, err := repo.ClaimNextRunner(ctx, transcriptstore.ClaimNextRunnerInput{RunnerID: "reclaimed-runner", TTL: time.Minute})
	if err != nil || !current.Claimed || current.Claim.ClaimedInputRevision != initial.Claim.ClaimedInputRevision {
		t.Fatalf("normal old-input reclaim=%+v err=%v", current, err)
	}
	const messageID, message = "late-composer-input", "Review the additional evidence separately."
	if err := srv.prepareCompatibilityFrameMessageRuntime(frame, messageID,
		map[string]any{"request": message}, nil, nil, nil, nil, nil, "",
		compatibilityFrameMessageRuntimeOptions{MessageOrigin: "input_response"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.ensureExpiredTranscriptContinuationDispatch(ctx, frame); err != nil {
		t.Fatal(err)
	}
	if dispatch, found, err := store.GetCompatibilityFrameResumeDispatchByFrame(frameID); err != nil || found {
		t.Fatalf("fresh reclaimed runner unexpectedly had a dispatch: dispatch=%+v found=%t err=%v", dispatch, found, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(ctx, current.Claim); err != nil {
		t.Fatalf("reclaimed owner is not legal: %v", err)
	}
	authority := &transcriptRunnerAuthority{Stream: current.Stream, Claim: current.Claim}
	finish, err := srv.finishTranscriptRunner(ctx, authority, "completed", "The initial review is complete.")
	if err != nil || finish.EventID <= 0 {
		t.Fatalf("legal runner finish=%+v err=%v", finish, err)
	}
	var inputs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_input_response'
	AND client_message_id=? AND json_extract(payload_json,'$.messageUuid')=? AND json_extract(payload_json,'$.text')=?`,
		stream.UID, messageID, messageID, message).Scan(&inputs); err != nil || inputs != 1 {
		t.Fatalf("canonical new input count=%d err=%v", inputs, err)
	}
	pending, found, err := repo.GetFramePendingInputState(ctx, "local", frameID)
	if err != nil || !found || pending.InputRevision <= current.Claim.ClaimedInputRevision ||
		pending.ConsumedInputRevision != current.Claim.ClaimedInputRevision {
		t.Fatalf("new input was consumed by old finish: pending=%+v found=%t err=%v", pending, found, err)
	}
	updated, found, err := store.GetFrame(frameID)
	if err != nil || !found || updated.Status != "processing" {
		t.Errorf("old finish terminalized new composer input: frame=%+v found=%t err=%v", updated, found, err)
	}
	next, err := repo.ClaimNextRunner(ctx, transcriptstore.ClaimNextRunnerInput{RunnerID: "followup-runner", TTL: time.Minute})
	if err != nil || !next.Claimed || next.Claim.ClaimedInputRevision != pending.InputRevision {
		t.Errorf("new composer input_response is not schedulable: claimed=%t revision=%d want=%d err=%v",
			next.Claimed, next.Claim.ClaimedInputRevision, pending.InputRevision, err)
	}
}
