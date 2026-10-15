package workspace

import (
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func admissionContinueInput(t *testing.T, store *Store, stream transcriptstore.Stream) ResumeCompatibilityFrameInput {
	t.Helper()
	target, err := store.GetCompatibilityFrameResumeTarget(t.Context(), stream.OwnerID, stream.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	return ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{ActorID: stream.OwnerID,
		ClientCommandID: "9c1bf953-1cf9-4af4-b0dd-150d29b42afe", Target: target}}
}

func TestCompletedUnknownInputContinuesOnceWithoutGuessingCoverage(t *testing.T) {
	store, repo, stream := admissionLegacyV76Fixture(t)
	requires, err := repo.FramePendingInputRequiresContinue(t.Context(), stream.OwnerID, stream.FrameID)
	if err != nil || !requires {
		t.Fatalf("requires=%t err=%v", requires, err)
	}
	if auto, err := repo.ClaimNextRunner(t.Context(), transcriptstore.ClaimNextRunnerInput{RunnerID: "must-not-auto-resurrect", TTL: time.Minute}); err != nil || auto.Claimed {
		t.Fatalf("automatic=%+v err=%v", auto, err)
	}
	input := admissionContinueInput(t, store, stream)
	first, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, input)
	if err != nil || first.ContinueCommand == nil {
		t.Fatalf("continue=%+v err=%v", first, err)
	}
	for retry := 0; retry < 2; retry++ {
		again, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, input)
		if err != nil || again.Event.ID != first.Event.ID {
			t.Fatalf("retry=%+v err=%v", again, err)
		}
	}
	beforeClaim, err := repo.GetStream(t.Context(), stream.UID, stream.OwnerID)
	if err != nil || beforeClaim.ConsumedInputRevision != 1 || beforeClaim.InputRevision != 2 {
		t.Fatalf("Continue consumed input before execution: %+v %v", beforeClaim, err)
	}
	dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("legacy-continuation-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("dispatch=%+v found=%t err=%v", dispatch, found, err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision != 2 {
		t.Fatalf("normal prefix claim=%+v err=%v", claimed, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(t.Context(), claimed.Claim); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CancelFrameWithTranscript(t.Context(), stream.FrameID); err != nil {
		t.Fatal(err)
	}
	if flag, err := repo.FramePendingInputRequiresContinue(t.Context(), stream.OwnerID, stream.FrameID); err != nil || flag {
		t.Fatalf("cancel flag=%t err=%v", flag, err)
	}
	if replay, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, input); err != nil || replay.Event.ID != first.Event.ID {
		t.Fatalf("cancelled command replay changed identity: %+v %v", replay, err)
	}
	frame, found, err := store.GetFrame(stream.FrameID)
	if err != nil || !found || frame.Status != FrameStatusCancelled {
		t.Fatalf("replay resurrected cancellation: %+v %v", frame, err)
	}
}

func TestPendingInputContinueTargetRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"input", "consumed", "branch", "owner"} {
		t.Run(change, func(t *testing.T) {
			store, repo, stream := admissionLegacyV76Fixture(t)
			input := admissionContinueInput(t, store, stream)
			switch change {
			case "input":
				_, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
					ClientMessageID: "new-input", MessageUUID: "new-input", Text: "New explicit input", DeferFrameActivation: true})
				if err != nil {
					t.Fatal(err)
				}
			case "consumed":
				// Controlled concurrent-state injection isolates C in the opaque
				// target fingerprint. It is not a runtime settlement technique.
				if _, err := store.db.Exec(`UPDATE transcript_streams SET consumed_input_revision=input_revision WHERE stream_uid=?`, stream.UID); err != nil {
					t.Fatal(err)
				}
			case "branch":
				state, err := repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = repo.AppendFrameUserEventToBranch(t.Context(), transcriptstore.AppendFrameUserEventToBranchInput{
					AppendFrameUserEventInput: transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "branch-input", MessageUUID: "branch-input", Text: "Continue this branch"},
					TargetBranchID:            state.ActiveBranchID, ExpectedActiveBranchID: state.ActiveBranchID, ExpectedGeneration: state.Generation, ClientMutationID: "continue-branch"})
				if err != nil {
					t.Fatal(err)
				}
			case "owner":
				input.Command.ActorID = "foreign-owner"
			}
			_, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, input)
			if change == "owner" {
				if err == nil {
					t.Fatal("foreign owner continued input")
				}
			} else if !errors.Is(err, ErrFrameResumeTargetStale) {
				t.Fatalf("changed %s error=%v", change, err)
			}
			var commands int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type='frame_resume_continue_command_accepted'`, stream.FrameID).Scan(&commands); err != nil || commands != 0 {
				t.Fatalf("stale command committed: %d %v", commands, err)
			}
		})
	}
}
