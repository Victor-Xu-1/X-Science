package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func testFrozenMessageIntent(t *testing.T, store *Store, mutation string) transcriptstore.FrameMessageIntent {
	t.Helper()
	frame, found, err := store.GetFrame("frame-a")
	if err != nil || !found {
		t.Fatalf("frame: %t %v", found, err)
	}
	return transcriptstore.FrameMessageIntent{Version: 1, OwnerID: "owner-a", FrameID: frame.ID,
		FrameIncarnationID: frame.IncarnationID, LoadingID: mutation,
		InputMessageID: transcriptstore.FrameMessageIntentID(frame.ID, mutation, ""), RequestSHA256: strings.Repeat("a", 64)}
}

func TestMessageIntentCancelBeforeAcceptanceFencesLateAppendAndQueue(t *testing.T) {
	store, repo, stream, claim := newTranscriptCancellationFixture(t)
	intent := testFrozenMessageIntent(t, store, "pending-followup")
	intent.OwnerID = stream.OwnerID
	ctx := context.Background()
	realtime, found, err := store.GetFrameRealtimeContextWithContext(ctx, intent.FrameID)
	if err != nil || !found || realtime.UserID != intent.OwnerID ||
		realtime.Frame.IncarnationID == "" || realtime.Frame.IncarnationID != intent.FrameIncarnationID {
		t.Fatalf("realtime read lost the frozen frame identity: %#v %t %v", realtime, found, err)
	}
	first, err := store.CancelFrameMessageIntent(ctx, intent)
	if err != nil || first.Receipt.Accepted || !first.Receipt.Cancelled || first.Receipt.CancellationStatus != "cancelled_before_acceptance" || len(first.CancelledFrameIDs) != 0 {
		t.Fatalf("preaccept: %#v %v", first, err)
	}
	second, err := store.CancelFrameMessageIntent(ctx, intent)
	if err != nil || !second.Replayed || second.Receipt != first.Receipt {
		t.Fatalf("replay: %#v %v", second, err)
	}
	_, _, _, err = repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: intent.InputMessageID,
		FrameEventID: "late", MessageUUID: intent.InputMessageID, Text: "pending", MessageIntent: &intent,
	})
	if !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("late append: %v", err)
	}
	payload := map[string]any{"text": "pending", "messageIntent": intent}
	if _, _, _, err := store.QueueCompatibilityMessage(intent.FrameID, intent.InputMessageID, payload, payload); !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("late queue: %v", err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(ctx, claim); err != nil {
		t.Fatalf("cancel touched other input: %v", err)
	}
	changed := intent
	changed.RequestSHA256 = strings.Repeat("b", 64)
	if _, err := store.CancelFrameMessageIntent(ctx, changed); !errors.Is(err, transcriptstore.ErrMessageIntentConflict) {
		t.Fatalf("changed payload: %v", err)
	}
}

func TestMessageIntentCancellationRetainsOnlyTypedReceipt(t *testing.T) {
	store, _, stream, _ := newTranscriptCancellationFixture(t)
	intent := testFrozenMessageIntent(t, store, "retained-message")
	intent.OwnerID = stream.OwnerID
	if _, err := store.CancelFrameMessageIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCompatibilityDeliveredIntent(stream.FrameID, "ordinary", map[string]any{"request": "ordinary"}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE queued_user_messages SET resolved_at=?`, time.Now().UTC().Add(-9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	report, err := store.SweepDataLifecycle(context.Background(), DefaultDataLifecyclePolicy())
	if err != nil || report.ResolvedQueuedIntents != 1 {
		t.Fatalf("retention: %#v %v", report, err)
	}
	if _, found, err := store.GetCompatibilityMessageIntent(intent.InputMessageID); err != nil || !found {
		t.Fatalf("lost tombstone: %t %v", found, err)
	}
	if _, err := store.db.Exec(`DELETE FROM frames WHERE id=?`, intent.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.GetCompatibilityMessageIntent(intent.InputMessageID); err != nil || found {
		t.Fatalf("Frame FK changed: %t %v", found, err)
	}
}

func TestMessageIntentExistingQueueRetractionUsesSameFence(t *testing.T) {
	store, repo, _, claim := newTranscriptCancellationFixture(t)
	intent := testFrozenMessageIntent(t, store, "ordinary-queue-delete")
	payload := map[string]any{"text": "Frozen followup", "messageIntent": intent}
	if _, _, _, err := store.QueueCompatibilityMessage(intent.FrameID, intent.InputMessageID, payload, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetractCompatibilityQueuedMessage(intent.FrameID, intent.InputMessageID); err != nil {
		t.Fatal(err)
	}
	result, err := store.CancelFrameMessageIntent(context.Background(), intent)
	if err != nil || !result.Replayed || result.Receipt.Accepted || !result.Receipt.Cancelled {
		t.Fatalf("queue retraction proof: %#v %v", result, err)
	}
	if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
		t.Fatalf("queue retraction touched current run: %v", err)
	}
}

func TestMessageIntentCancelledInputCannotReopenThroughResumeDispatch(t *testing.T) {
	store, repo, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	intent := testFrozenMessageIntent(t, store, "deferred-resume")
	failed := "failed"
	if _, err := store.UpdateFrame(intent.FrameID, UpdateFrameInput{Status: &failed}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: intent.InputMessageID, MessageUUID: intent.InputMessageID,
		FrameEventID: "deferred-event", Text: "Frozen deferred continuation", MessageIntent: &intent, DeferFrameActivation: true,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := store.CancelFrameMessageIntent(context.Background(), intent)
	if err != nil || result.Receipt.CancellationStatus != "already_terminal" || !result.Receipt.Accepted || result.Receipt.Cancelled {
		t.Fatalf("terminal receipt: %#v %v", result, err)
	}
	if _, err := store.ResumeCompatibilityFrameConversation(intent.FrameID, ResumeCompatibilityFrameInput{MessageIntent: &intent}); !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("late terminal resume: %v", err)
	}
	if _, err := store.CreateMessageAutoResumeDispatch(intent.FrameID, intent.FrameID, "project-a", "agent", "explicit_user_continue", intent); !errors.Is(err, transcriptstore.ErrMessageIntentCancelled) {
		t.Fatalf("late auto resume: %v", err)
	}
	frame, _, err := store.GetFrame(intent.FrameID)
	if err != nil || frame.Status != "failed" {
		t.Fatalf("cancelled intent reopened: %#v %v", frame, err)
	}
}
