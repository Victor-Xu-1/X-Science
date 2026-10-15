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

func TestPendingInputContinuationDoesNotMaskLiveOwnerOrCorruptSource(t *testing.T) {
	for _, boundary := range []string{"live_owner", "corrupt_source"} {
		t.Run(boundary, func(t *testing.T) {
			store, repo, stream := admissionLegacyV76Fixture(t)
			input := admissionContinueInput(t, store, stream)
			if boundary == "live_owner" {
				now := time.Now().UTC()
				if _, err := store.db.Exec(`INSERT INTO frames(id,incarnation_id,project_id,root_frame_id,parent_frame_id,agent_name,status,conversation_type,created_at,updated_at)
					VALUES('live-child','46ebcf21-cdf7-41a2-9ed0-6087011bcd59',?,? ,?,'OPERON','processing','agent',?,?)`, stream.ProjectID, stream.RootFrameID, stream.FrameID, now, now); err != nil {
					t.Fatal(err)
				}
				child, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:live-child", OwnerID: stream.OwnerID,
					ExternalID: "live-child", SessionID: "live-child", Kind: transcriptstore.StreamKindFrameRef, FrameID: "live-child", RootFrameID: stream.RootFrameID, ProjectID: stream.ProjectID, Epoch: 1})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: child.UID, OwnerID: child.OwnerID,
					ClientMessageID: "child-input", MessageUUID: "child-input", Text: "Work under this root"}); err != nil {
					t.Fatal(err)
				}
				claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: child.UID, OwnerID: child.OwnerID,
					RunnerID: "live-child-owner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
				if err != nil || !claim.Claimed {
					t.Fatalf("live child claim=%+v %v", claim, err)
				}
			} else {
				if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
					ClientMessageID: "exact-but-corrupt", MessageUUID: "exact-but-corrupt", Text: "Retained input", DeferFrameActivation: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := store.db.Exec(`DROP TRIGGER transcript_input_admissions_immutable;
					UPDATE transcript_input_admissions SET input_kind='control' WHERE stream_uid=?`, stream.UID); err != nil {
					t.Fatal(err)
				}
				// Refresh the target to isolate source verification, not staleness.
				input = admissionContinueInput(t, store, stream)
			}
			flag, err := repo.FramePendingInputRequiresContinue(t.Context(), stream.OwnerID, stream.FrameID)
			if flag || boundary == "live_owner" && err != nil || boundary == "corrupt_source" && !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatalf("%s flag=%t err=%v", boundary, flag, err)
			}
			if _, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, input); err == nil {
				t.Fatalf("%s accepted Continue", boundary)
			}
		})
	}
}
