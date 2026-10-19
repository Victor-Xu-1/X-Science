package server

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

func specialistInputFixture(mutation sessionagent.Mutation, stream transcriptstore.Stream, id string) transcriptstore.AppendFrameUserEventInput {
	return transcriptstore.AppendFrameUserEventInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: id,
		FrameEventID: id + "-event", MessageUUID: id + "-message", MessageOrigin: "task_intent", Text: "Synthetic immutable input " + id,
		InputData: map[string]any{sessionagent.InputReferenceKey: sessionagent.InputReference{
			Scope: mutation.Snapshot.Scope, Branch: mutation.Snapshot.PreparedBranch, Revision: mutation.Snapshot.Revision, SnapshotID: mutation.Snapshot.Desired.SnapshotID,
		}},
	}
}

func specialistClaimFixture(t *testing.T, repo *transcriptstore.Repository, stream transcriptstore.Stream, id string) transcriptstore.RunnerClaim {
	t.Helper()
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: id, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	return claimed.Claim
}

func TestSessionSpecialistAcceptedInputAndRecoveredSourceStayImmutable(t *testing.T) {
	app, store, frame := newSessionSpecialistAPIFixture(t)
	first := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_A", "source-prepare-a", false)
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{UID: "frame:" + frame.ID, OwnerID: specialistFixtureOwner,
		ExternalID: frame.ID, SessionID: frame.ID, Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := specialistInputFixture(first, stream, "source-input-a")
	_, event, created, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || !created {
		t.Fatalf("first input=%t err=%v", created, err)
	}
	claimA := specialistClaimFixture(t, repo, stream, "source-runner-a")
	snapshotA, err := repo.ClaimedRunnerAgentSnapshot(t.Context(), claimA)
	if err != nil || snapshotA == nil || snapshotA.SelectionName != "SYNTHETIC_A" {
		t.Fatalf("claimed A=%#v err=%v", snapshotA, err)
	}
	second := specialistPrepareFixture(t, app, frame.ID, "SYNTHETIC_B", "source-prepare-b", false)
	_, replayed, recreated, err := repo.AppendFrameUserEvent(t.Context(), input)
	if err != nil || recreated || replayed.ID != event.ID {
		t.Fatalf("accepted message replay=%#v created=%t err=%v", replayed, recreated, err)
	}
	stale := input
	stale.ClientMessageID = "stale-input"
	stale.FrameEventID = "stale-event"
	stale.MessageUUID = "stale-message"
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), stale); !errors.Is(err, sessionagent.ErrRevisionConflict) {
		t.Fatalf("new stale selection accepted: %v", err)
	}
	_, sourceEvent, _, err := repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimA, ClientMessageID: "source-a-checkpoint", Phase: transcriptstore.RunnerPhasePlanning, Resumable: true, PayloadJSON: []byte(`{"status":"running","message":"source A boundary"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSessionSpecialistPreparation(t.Context(), claimA, snapshotA.ID, true); err != nil {
		t.Fatal(err)
	}
	state := specialistReadbackFixture(t, app, frame.ID)
	if state.Desired.SelectionName != "SYNTHETIC_B" || state.Applied.SelectionName != "SYNTHETIC_A" || state.Phase != "prepared" {
		t.Fatalf("actual versus desired merged: %#v", state)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{Claim: claimA, ClientMessageID: "finish-source-a", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSessionSpecialistPreparation(t.Context(), claimA, snapshotA.ID, true); err == nil {
		t.Fatal("finished old claim retained mutation authority")
	}
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), specialistInputFixture(second, stream, "source-input-b")); err != nil {
		t.Fatal(err)
	}
	claimB := specialistClaimFixture(t, repo, stream, "source-runner-b")
	snapshotB, err := repo.ClaimedRunnerAgentSnapshot(t.Context(), claimB)
	if err != nil || snapshotB == nil || snapshotB.SelectionName != "SYNTHETIC_B" {
		t.Fatalf("claimed B=%#v err=%v", snapshotB, err)
	}
	origin := transcriptstore.AgentOperationSource{StreamUID: stream.UID, FrameID: frame.ID, EventID: sourceEvent.EventID, Attempt: claimA.Attempt}
	source, err := repo.ClaimedRunnerAgentOperationSource(t.Context(), claimB, origin)
	if err != nil || !source.Managed || source.Snapshot == nil || source.Snapshot.ID != snapshotA.ID {
		t.Fatalf("recovery inherited new specialist: %#v err=%v", source, err)
	}
	if reference, err := sessionagent.InputReferenceFrom(source.InputData); err != nil || reference == nil || reference.SnapshotID != snapshotA.ID {
		t.Fatalf("recovery inherited new composer data: %#v err=%v", source.InputData, err)
	}
	run := &sessionRunnerChatRun{SessionID: stream.SessionID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimB}}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	projected, _, err := app.specialistOperationContext(ctx, run, workspace.KernelLocalOperation{OwnerUserID: stream.OwnerID, FrameID: frame.ID,
		StreamUID: stream.UID, SourceEventID: sourceEvent.EventID, SourceRunnerAttempt: claimA.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := app.claimedSessionSpecialistForFrame(projected, stream.OwnerID, frame.ID)
	if err != nil || selected == nil || selected.ID != snapshotA.ID {
		t.Fatalf("kernel source projection=%#v err=%v", selected, err)
	}
	if _, err := app.claimedSessionSpecialistForFrame(projected, "foreign-owner", frame.ID); err == nil {
		t.Fatal("foreign source context was accepted")
	}
	selected, err = app.claimedSessionSpecialistForFrame(ctx, stream.OwnerID, frame.ID)
	if err != nil || selected == nil || selected.ID != snapshotB.ID {
		t.Fatal("operation projection mutated the normal runner binding")
	}
}
