package workspace

import (
	"errors"
	"testing"
	"time"
)

func TestFrameContinueTargetRejectsNewResumeCycleAfterClockRollback(t *testing.T) {
	store := newResumeDescriptionStore(t)
	const frameID = "command-clock-rollback"
	if _, err := store.CreateFrame(CreateFrameInput{ID: frameID, ProjectID: "project-resume-description", AgentName: "OPERON", Status: "failed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC().Add(2 * time.Hour)
	store.now = func() time.Time { return clock }
	completeCycle := func() {
		t.Helper()
		if _, err := store.ResumeCompatibilityFrameConversation(frameID, ResumeCompatibilityFrameInput{}); err != nil {
			t.Fatal(err)
		}
		claim, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("controlled-clock-worker", time.Minute)
		if err != nil || !found {
			t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
		}
		if _, _, err := store.CompleteCompatibilityFrameResumeDispatch(CompleteCompatibilityFrameResumeDispatchInput{
			ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken, Status: "failed",
		}); err != nil {
			t.Fatal(err)
		}
	}
	completeCycle()
	original, err := store.GetCompatibilityFrameResumeTarget(t.Context(), "owner-resume-description", frameID)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(-time.Hour)
	completeCycle()
	_, err = store.ResumeCompatibilityFrameConversation(frameID, ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{
		ActorID: "owner-resume-description", ClientCommandID: "8598a275-18e9-4f5c-bd0b-2f9f0a5907c1", Target: original,
	}})
	if !errors.Is(err, ErrFrameResumeTargetStale) {
		t.Fatalf("old first-use target accepted a newer completed resume cycle after clock rollback: %v", err)
	}
	frame, found, err := store.GetFrame(frameID)
	if err != nil || !found || frame.Status != "failed" {
		t.Fatalf("stale command changed frame=%+v found=%t err=%v", frame, found, err)
	}
}
