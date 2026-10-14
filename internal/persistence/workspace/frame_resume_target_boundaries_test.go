package workspace

import (
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestFrameContinueTargetSeparatesPassiveFactsFromCanonicalChanges(t *testing.T) {
	_, store, repo, stream, _, _ := newFrameContinueCanonicalFixture(t)
	read := func() CompatibilityFrameResumeTarget {
		t.Helper()
		target, err := store.GetCompatibilityFrameResumeTarget(t.Context(), stream.OwnerID, stream.FrameID)
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	initial := read()
	if _, err := store.AppendFrameEvent(FrameEventInput{FrameID: stream.FrameID, Type: "controlled_diagnostic", Payload: map[string]any{"observed": true}}); err != nil {
		t.Fatal(err)
	}
	name := "Updated presentation only"
	if _, err := store.UpdateCompatibilityFrame(stream.FrameID, UpdateCompatibilityFrameInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if passive := read(); passive.StateRef != initial.StateRef {
		t.Fatalf("passive facts invalidated target: %+v %+v", initial, passive)
	}
	assertStale := func(old CompatibilityFrameResumeTarget) {
		t.Helper()
		_, err := store.ResumeCompatibilityFrameConversation(stream.FrameID, ResumeCompatibilityFrameInput{AllowProcessingInterrupted: true, Command: &CompatibilityFrameContinueCommand{
			ActorID: stream.OwnerID, ClientCommandID: "f6fd3058-1e9a-4d5d-8b51-4784cc065b7a", Target: old,
		}})
		if !errors.Is(err, ErrFrameResumeTargetStale) {
			t.Fatalf("canonical change accepted old target: %v", err)
		}
	}
	if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "command-next-input", FrameEventID: "command-next-input-frame", MessageUUID: "command-next-input-message", Text: "New explicit input",
	}); err != nil || !created {
		t.Fatalf("new input created=%t err=%v", created, err)
	}
	assertStale(initial)
	afterInput := read()
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "command-next-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if heartbeat, err := repo.HeartbeatRunner(t.Context(), transcriptstore.HeartbeatRunnerInput{Claim: claim.Claim, TTL: time.Minute}); err != nil || !heartbeat.Renewed {
		t.Fatalf("heartbeat=%+v err=%v", heartbeat, err)
	}
	if passive := read(); passive.StateRef != afterInput.StateRef {
		t.Fatalf("runner claim/heartbeat invalidated target: %+v %+v", afterInput, passive)
	}
	if _, _, _, err := repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: claim.Claim, ClientMessageID: "command-next-checkpoint", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: []byte(`{"controlled":true}`)}); err != nil {
		t.Fatal(err)
	}
	assertStale(afterInput)
	beforeBranch := read()
	branch, err := repo.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, SourceBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID, ExpectedGeneration: branch.Generation,
		ClientMutationID: "command-new-branch", SourceClientMessageID: "command-next-input", SourceMessageIndex: 1, ReplacementText: "New branch explicit input",
		Destinations: []string{"ws"},
	}); err != nil {
		t.Fatal(err)
	}
	assertStale(beforeBranch)
	var commands int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&commands); err != nil || commands != 0 {
		t.Fatalf("stale commands persisted=%d err=%v", commands, err)
	}
}
