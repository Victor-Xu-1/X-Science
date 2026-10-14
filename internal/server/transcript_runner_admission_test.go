package server

import (
	"fmt"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerAdmissionFactsDoNotManufactureWebActivity(t *testing.T) {
	attempt := int64(1)
	for _, admission := range []struct{ kind, identity, payload string }{
		{transcriptstore.RunnerPreparationAdmissionEventType, "runner-preparation:stream-a:1", `{"dispatch_state":"preparation"}`},
		{"runner_checkpoint", "runner-execution-admitted:stream-a:1", `{"status":"running","dispatch_state":"execution_admitted"}`},
		{transcriptstore.RunnerTimingResumeEventType, "runner-timing-resume:cloned-or-damaged", `{"schema":"unknown","stream_uid":"original-source"}`},
	} {
		t.Run(admission.kind, func(t *testing.T) {
			projected := transcriptstore.ProjectedEvent{Event: transcriptstore.Event{
				StreamUID: "stream-a", Type: admission.kind, Source: transcriptstore.EventSourcePayload,
				RunnerAttempt: &attempt, ClientMessageID: admission.identity,
			}, ResolvedPayloadJSON: []byte(admission.payload)}
			if disposition := classifyTranscriptWebIncrementalEvent(projected); disposition != "" {
				t.Fatalf("internal admission forced full projection: %s", disposition)
			}
			messages := []map[string]any{}
			if err := (&Server{}).projectTranscriptWebProjectedEvent(t.Context(), &messages, nil, nil, nil, nil,
				transcriptstore.Stream{}, "owner-a", "session-a", projected); err != nil || len(messages) != 0 {
				t.Fatalf("admission created visible activity: messages=%#v error=%v", messages, err)
			}
			reducer := newTranscriptWebCoordinateReducer("session-a", false)
			if err := reducer.consume(projected); err != nil || len(reducer.coordinates) != 0 {
				t.Fatalf("admission created a visible cursor: coordinates=%#v error=%v", reducer.coordinates, err)
			}
		})
	}
}

func TestRunnerAdmissionFactsAdvanceIncrementalCursorWithoutMessageGaps(t *testing.T) {
	store, repository, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-admission", "project-admission", "frame-admission")
	stream, err := repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "stream-admission", OwnerID: "owner-admission", ExternalID: "frame-admission", SessionID: "frame-admission",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-admission", RootFrameID: "frame-admission", FrameID: "frame-admission", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendInput := func(identity string) {
		t.Helper()
		if _, _, created, err := repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: identity,
			FrameEventID: identity + "-frame", MessageUUID: identity, Text: identity,
		}); err != nil || !created {
			t.Fatalf("input created=%t error=%v", created, err)
		}
	}
	appendInput("first-input")
	branch, err := repository.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	readModel := transcriptstore.NewWebReadModelRepository(db, db)
	server := &Server{workspaceStore: store, transcriptStore: repository, transcriptWebReadModel: readModel}
	if err := server.rebuildTranscriptWebReadModel(t.Context(), requireSingleTranscriptWebProjectionWork(t, readModel, stream.OwnerID)); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "preparation-worker", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claimed=%#v error=%v", claimed, err)
	}
	if _, _, _, err := repository.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimed.Claim, ClientMessageID: fmt.Sprintf("runner-execution-admitted:%s:%d", stream.UID, claimed.Claim.Attempt),
		Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: []byte(`{"status":"running","dispatch_state":"execution_admitted"}`),
	}); err != nil {
		t.Fatal(err)
	}
	appendInput("second-input")
	result, err := server.tryIncrementalTranscriptWebReadModel(t.Context(), stream, requireSingleTranscriptWebProjectionWork(t, readModel, stream.OwnerID))
	if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady {
		t.Fatalf("admission forced fallback: result=%#v error=%v", result, err)
	}
	fence := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
	if fence.StateMessageCount != 2 || fence.VisibleMessageCount != 2 || fence.StateThroughPublicationSequence != 4 {
		t.Fatalf("internal facts changed message ordinals or failed to advance cursor: %#v", fence)
	}
	_, records, found, err := readModel.GetTranscriptWebProjectionCheckpoint(t.Context(), stream.OwnerID, stream.UID, branch.ActiveBranchID)
	if err != nil || !found || len(records) != 2 || records[1].Ordinal != 2 {
		t.Fatalf("message gap: records=%#v found=%t error=%v", records, found, err)
	}
}

func TestRunnerTimingFactsAdvanceAnInternalOnlyPageWithoutPublicActivity(t *testing.T) {
	store, repository, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "owner-clock", "project-clock", "frame-clock")
	stream, err := repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "stream-clock", OwnerID: "owner-clock", ExternalID: "frame-clock", SessionID: "frame-clock",
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-clock", RootFrameID: "frame-clock", FrameID: "frame-clock", Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendInput := func(identity string) {
		t.Helper()
		if _, _, created, err := repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: identity,
			FrameEventID: identity + "-frame", MessageUUID: identity, Text: identity,
		}); err != nil || !created {
			t.Fatalf("input created=%t err=%v", created, err)
		}
	}
	appendInput("clock-first-input")
	claim, err := repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "clock-first", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	paused, err := repository.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{Claim: claim.Claim, ClientMessageID: "clock-pause", ReasonCode: "runtime_draining", Resumable: true, AutoResume: true})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := repository.GetBranchState(t.Context(), stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	readModel := transcriptstore.NewWebReadModelRepository(db, db)
	server := &Server{workspaceStore: store, transcriptStore: repository, transcriptWebReadModel: readModel}
	if err := server.rebuildTranscriptWebReadModel(t.Context(), requireSingleTranscriptWebProjectionWork(t, readModel, stream.OwnerID)); err != nil {
		t.Fatal(err)
	}
	before := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
	resumed, err := repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "clock-resume", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: paused.Checkpoint.Sequence})
	if err != nil || !resumed.Claimed {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	result, err := server.tryIncrementalTranscriptWebReadModel(t.Context(), stream, requireSingleTranscriptWebProjectionWork(t, readModel, stream.OwnerID))
	if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady || result.MessagesWritten != 0 {
		t.Fatalf("internal-only page=%+v err=%v", result, err)
	}
	afterFact := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
	if afterFact.StateThroughPublicationSequence != before.StateThroughPublicationSequence+1 || afterFact.VisibleMessageCount != before.VisibleMessageCount || afterFact.StateMessageCount != before.StateMessageCount {
		t.Fatalf("internal fact manufactured a message or stalled cursor: before=%+v after=%+v", before, afterFact)
	}
	appendInput("clock-second-input")
	result, err = server.tryIncrementalTranscriptWebReadModel(t.Context(), stream, requireSingleTranscriptWebProjectionWork(t, readModel, stream.OwnerID))
	if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady {
		t.Fatalf("following page=%+v err=%v", result, err)
	}
	afterInput := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
	if afterInput.StateMessageCount != before.StateMessageCount+1 || afterInput.VisibleMessageCount != before.VisibleMessageCount+1 || afterInput.StateThroughPublicationSequence != afterFact.StateThroughPublicationSequence+1 {
		t.Fatalf("next page inherited a visible ordinal gap: before=%+v after=%+v", before, afterInput)
	}
}
