package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func appendFrozenTestMessage(t *testing.T, repo *transcriptstore.Repository, stream transcriptstore.Stream, intent transcriptstore.FrameMessageIntent) {
	t.Helper()
	_, _, created, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: intent.InputMessageID,
		MessageUUID: intent.InputMessageID, FrameEventID: "event:" + intent.InputMessageID,
		Text: "Frozen followup", MessageIntent: &intent,
	})
	if err != nil || !created {
		t.Fatalf("append: %t %v", created, err)
	}
}

func TestMessageIntentAcceptedCancellationUsesCanonicalInputAndDoesNotReplay(t *testing.T) {
	store, repo, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	intent := testFrozenMessageIntent(t, store, "accepted-followup")
	appendFrozenTestMessage(t, repo, stream, intent)
	claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "intent-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim: %#v %v", claim, err)
	}
	result, err := store.CancelFrameMessageIntent(context.Background(), intent)
	if err != nil || !result.Receipt.Accepted || !result.Receipt.Cancelled || result.Receipt.CancellationStatus != "cancelled" || len(result.CancelledFrameIDs) != 1 {
		t.Fatalf("accepted cancel: %#v %v", result, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim.Claim); err == nil {
		t.Fatal("cancelled claim still live")
	}
	_, _, _, err = repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: intent.InputMessageID,
		MessageUUID: intent.InputMessageID, FrameEventID: "event:" + intent.InputMessageID, Text: "Frozen followup", MessageIntent: &intent,
	})
	if !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("cancelled accept replay: %v", err)
	}
	newer := testFrozenMessageIntent(t, store, "newer-owned-input")
	appendFrozenTestMessage(t, repo, stream, newer)
	replay, err := store.CancelFrameMessageIntent(context.Background(), intent)
	if err != nil || !replay.Replayed || len(replay.CancelledFrameIDs) != 0 {
		t.Fatalf("old receipt replay: %#v %v", replay, err)
	}
	frame, _, err := store.GetFrame(intent.FrameID)
	if err != nil || frame.Status != "processing" {
		t.Fatalf("old receipt stopped new input: %#v %v", frame, err)
	}
}

func TestMessageIntentQueuedCancellationDoesNotStopRunningInput(t *testing.T) {
	for _, delivering := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "delivery-claimed"}[delivering], func(t *testing.T) {
			store, repo, _, claim := newTranscriptCancellationFixture(t)
			intent := testFrozenMessageIntent(t, store, "queued-followup")
			payload := map[string]any{"text": "Frozen followup", "messageIntent": intent}
			if _, _, _, err := store.QueueCompatibilityMessage(intent.FrameID, intent.InputMessageID, payload, payload); err != nil {
				t.Fatal(err)
			}
			if delivering {
				if _, err := store.ClaimCompatibilityQueuedMessages(intent.FrameID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := store.CancelFrameMessageIntent(context.Background(), intent)
			if err != nil || result.Receipt.Accepted || !result.Receipt.Cancelled || len(result.CancelledFrameIDs) != 0 {
				t.Fatalf("queued cancel: %#v %v", result, err)
			}
			if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
				t.Fatalf("unrelated runner stopped: %v", err)
			}
			_ = store.ReleaseCompatibilityQueuedMessageDelivery(intent.InputMessageID)
			record, found, err := store.GetCompatibilityMessageIntent(intent.InputMessageID)
			if err != nil || !found || record.State != "retracted" {
				t.Fatalf("release resurrected: %#v %v", record, err)
			}
		})
	}
}

func TestMessageIntentCancellationRejectsNewerQueueAndHistoricalProof(t *testing.T) {
	store, repo, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	intent := testFrozenMessageIntent(t, store, "accepted-stale")
	appendFrozenTestMessage(t, repo, stream, intent)
	if _, _, _, err := store.QueueCompatibilityMessage(intent.FrameID, "newer", map[string]any{"text": "newer"}, map[string]any{"text": "newer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelFrameMessageIntent(context.Background(), intent); !errors.Is(err, ErrFrameCancellationInputStale) {
		t.Fatalf("newer queue: %v", err)
	}
	if _, found, err := store.GetCompatibilityMessageIntent(intent.InputMessageID); err != nil || found {
		t.Fatalf("failed cancellation wrote receipt: %t %v", found, err)
	}
	legacy := testFrozenMessageIntent(t, store, "legacy-input")
	if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: legacy.InputMessageID,
		MessageUUID: legacy.InputMessageID, FrameEventID: "legacy-event", Text: "Historical no proof",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelFrameMessageIntent(context.Background(), legacy); !errors.Is(err, transcriptstore.ErrMessageIntentUnavailable) {
		t.Fatalf("legacy proof guessed: %v", err)
	}
}

func TestMessageIntentBranchLateAppendRollsBackAndScopeIsBound(t *testing.T) {
	store, repo, stream, claim := newTranscriptCancellationFixture(t)
	ctx := context.Background()
	branch, err := repo.GetBranchState(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	intent := testFrozenMessageIntent(t, store, "branch-followup")
	intent.TargetBranchID = branch.ActiveBranchID
	intent.InputMessageID = transcriptstore.FrameMessageIntentID(intent.FrameID, intent.LoadingID, intent.TargetBranchID)
	if _, err := store.CancelFrameMessageIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	_, err = repo.AppendFrameUserEventToBranch(ctx, transcriptstore.AppendFrameUserEventToBranchInput{
		AppendFrameUserEventInput: transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID,
			ClientMessageID: intent.InputMessageID, MessageUUID: intent.InputMessageID, FrameEventID: "frame-branch-continue:" + intent.FrameID + ":" + intent.TargetBranchID + ":" + intent.LoadingID,
			Text: "Frozen followup", MessageIntent: &intent},
		TargetBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation, ClientMutationID: intent.LoadingID,
	})
	if !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("late branch: %v", err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(ctx, claim); err != nil {
		t.Fatalf("branch cancellation escaped rollback: %v", err)
	}
	current, err := repo.GetBranchState(ctx, stream.UID, stream.OwnerID)
	if err != nil || current.Generation != branch.Generation {
		t.Fatalf("branch changed: %#v %v", current, err)
	}
	wrongOwner := intent
	wrongOwner.OwnerID = "different-owner"
	if _, err := store.CancelFrameMessageIntent(ctx, wrongOwner); !errors.Is(err, transcriptstore.ErrOwnerMismatch) {
		t.Fatalf("owner mismatch: %v", err)
	}
	wrongIncarnation := intent
	wrongIncarnation.FrameIncarnationID = "replaced-frame"
	if _, err := store.CancelFrameMessageIntent(ctx, wrongIncarnation); !errors.Is(err, transcriptstore.ErrMessageIntentConflict) {
		t.Fatalf("incarnation mismatch: %v", err)
	}
	changedMode := intent
	changedMode.TargetBranchID = ""
	changedMode.InputMessageID = transcriptstore.FrameMessageIntentID(intent.FrameID, intent.LoadingID, "")
	if _, err := store.CancelFrameMessageIntent(ctx, changedMode); !errors.Is(err, transcriptstore.ErrMessageIntentConflict) {
		t.Fatalf("branch mode mismatch: %v", err)
	}
}

func TestMessageIntentCompletedAdmissionKeepsOriginalCompareAndSwap(t *testing.T) {
	store, repo, stream, claim := newTranscriptCancellationFixture(t)
	intent := testFrozenMessageIntent(t, store, "stale-completed-snapshot")
	_, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: intent.InputMessageID,
		MessageUUID: intent.InputMessageID, FrameEventID: "stale-completed-event", Text: "Frozen followup", MessageIntent: &intent, RequireCompletedFrame: true,
	})
	if !errors.Is(err, transcriptstore.ErrFrameMessageStateStale) {
		t.Fatalf("completed CAS lost: %v", err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
		t.Fatalf("stale completed snapshot touched running task: %v", err)
	}
}
