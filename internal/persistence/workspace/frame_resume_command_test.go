package workspace

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestFrameContinueCommandReplayDoesNotReactivateLaterFailure(t *testing.T) {
	store := newResumeDescriptionStore(t)
	const frameID = "command-terminal-replay"
	if _, err := store.CreateFrame(CreateFrameInput{ID: frameID, ProjectID: "project-resume-description", AgentName: "OPERON", Status: "failed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetCompatibilityFrameResumeTarget(t.Context(), "owner-resume-description", frameID)
	if err != nil {
		t.Fatal(err)
	}
	input := ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{
		ActorID: "owner-resume-description", ClientCommandID: "ea4e20dd-f886-40fa-8766-16c103022da9", Target: target,
	}}
	first, err := store.ResumeCompatibilityFrameConversation(frameID, input)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("command-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	if _, _, err := store.CompleteCompatibilityFrameResumeDispatch(CompleteCompatibilityFrameResumeDispatchInput{
		ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
		Status: "failed", Message: "later controlled failure",
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ResumeCompatibilityFrameConversation(frameID, input)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Event == nil || first.Event == nil || replay.Event.ID != first.Event.ID {
		t.Fatalf("same command reactivated a later failure: first=%+v replay=%+v", first.Event, replay.Event)
	}
	assertResumeDescription(t, store, frameID, "failed", "later controlled failure")
}

func newFrameContinueCanonicalFixture(t *testing.T) (string, *Store, *transcriptstore.Repository, transcriptstore.Stream, transcriptstore.ClaimRunnerResult, transcriptstore.InterruptRunnerResult) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "command-project", UserID: "command-owner", Name: "Command"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "command-frame", ProjectID: "command-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:command-frame", OwnerID: "command-owner", ExternalID: "command-frame", SessionID: "command-frame",
		Kind: transcriptstore.StreamKindFrameRef, FrameID: "command-frame", RootFrameID: "command-frame", ProjectID: "command-project", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "command-input", FrameEventID: "command-input-frame", MessageUUID: "command-input-message", Text: "Controlled input",
	}); err != nil || !created {
		t.Fatalf("input created=%t err=%v", created, err)
	}
	runner, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "command-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !runner.Claimed {
		t.Fatalf("runner=%+v err=%v", runner, err)
	}
	interrupted, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: runner.Claim, ClientMessageID: "command-park", ReasonCode: "controlled_wait", Resumable: true, AutoResume: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAutoResumeDispatch("command-frame", "command-frame", "command-project", "OPERON", "controlled_wait"); err != nil {
		t.Fatal(err)
	}
	return path, store, repo, stream, runner, interrupted
}

func TestFrameContinueTargetSurvivesRealBackstopAndRestart(t *testing.T) {
	path, store, _, stream, runner, interrupted := newFrameContinueCanonicalFixture(t)
	clock := time.Now().UTC()
	store.now = func() time.Time { return clock }
	var original CompatibilityFrameResumeTarget
	for round := 0; round < 4; round++ {
		clock = clock.Add(time.Minute)
		claim, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("command-backstop", time.Minute)
		if err != nil || !found {
			t.Fatalf("claim round=%d found=%t err=%v", round, found, err)
		}
		if round > 0 {
			claimedTarget, err := store.GetCompatibilityFrameResumeTarget(t.Context(), stream.OwnerID, stream.FrameID)
			if err != nil || claimedTarget.StateRef != original.StateRef {
				t.Fatalf("passive claim invalidated the saved target: %+v %v", claimedTarget, err)
			}
		}
		if _, _, err := store.RequeueCompatibilityFrameResumeDispatch(RequeueCompatibilityFrameResumeDispatchInput{
			ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
			ReasonCode: "controlled_wait", RunnerAttempt: int(runner.Claim.Attempt), CheckpointEventID: interrupted.Event.EventID,
			NotBefore: clock.Add(30 * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		current, err := store.GetCompatibilityFrameResumeTarget(t.Context(), stream.OwnerID, stream.FrameID)
		if err != nil || current.WaitRef == nil {
			t.Fatalf("target=%+v err=%v", current, err)
		}
		if round == 0 {
			original = current
		} else if current.StateRef != original.StateRef || *current.WaitRef == *original.WaitRef {
			t.Fatalf("backstop must change raw wait but retain semantic target: original=%+v current=%+v", original, current)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restoredStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store = restoredStore
	t.Cleanup(func() { _ = store.Close() })
	restored, err := store.GetCompatibilityFrameResumeTarget(context.Background(), stream.OwnerID, stream.FrameID)
	if err != nil || restored.StateRef != original.StateRef {
		t.Fatalf("restart changed target without a new semantic fact: %+v %v", restored, err)
	}
	continued, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{
		ActorID: stream.OwnerID, ClientCommandID: "b49119aa-d460-4d1e-b35b-742255ec7f13", Target: original,
	}})
	if err != nil || continued.ContinueCommand == nil || continued.ContinueCommand.Outcome != "woken" {
		t.Fatalf("same-wait original intent failed after backstops and restart: %+v %v", continued, err)
	}
}
