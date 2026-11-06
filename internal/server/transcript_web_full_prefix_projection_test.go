package server

import (
	"errors"
	"reflect"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebFullProjectionRetainsAppendOnlyPrefix(t *testing.T) {
	captured := captureTranscriptWebFullProjection(t)
	appendTranscriptWebFullProjectionTail(t, captured.fixture, "after-capture")
	if err := captured.fixture.server.visitTranscriptWebProjectionSnapshotRaw(t.Context(), captured.fixture.stream,
		captured.fixture.stream.OwnerID, captured.snapshot, true, func(transcriptstore.ProjectedEvent) error { return nil }); !errors.Is(err, transcriptstore.ErrBranchStateStale) {
		t.Fatalf("strict Raw consumer silently acquired append-tolerant semantics: %v", err)
	}
	if err := captured.finish(t); err != nil {
		t.Fatalf("append-only tail discarded the captured full prefix instead of retaining building progress: %v", err)
	}
	state, records, found, err := captured.fixture.readModel.GetTranscriptWebProjectionCheckpoint(
		t.Context(), captured.fixture.stream.OwnerID, captured.fixture.stream.UID, captured.snapshot.BranchID,
	)
	if err != nil || !found || state.Status != "building" ||
		state.ThroughPublicationSequence != captured.snapshot.ThroughPublicationSequence ||
		state.SourceRevision != captured.fence.SourceRevision || len(records) != len(captured.messages) {
		t.Fatalf("captured prefix was not retained as an exact non-ready checkpoint: found=%t state=%#v rows=%d err=%v", found, state, len(records), err)
	}
	fence := requireTranscriptWebReadModelFence(t, captured.fixture.readModel, captured.fixture.stream, captured.snapshot.BranchID)
	if transcriptWebFenceReady(fence) || fence.ThroughPublicationSequence <= state.ThroughPublicationSequence {
		t.Fatal("retained prefix was falsely ready before its tail was projected")
	}
	work := requireSingleTranscriptWebProjectionWork(t, captured.fixture.readModel, captured.fixture.stream.OwnerID)
	result, err := captured.fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), captured.fixture.stream, work)
	if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady || result.EventsRead != 1 {
		t.Fatalf("existing incremental owner did not consume only the one-event tail: result=%#v err=%v", result, err)
	}
	assertPreparationProjectionMatchesCanonical(t, captured.fixture)
	t.Logf("retained_prefix_through=%d tail_events_read=%d final_state=ready", state.ThroughPublicationSequence, result.EventsRead)
}

func TestTranscriptWebFullProjectionRejectsChangedPrefix(t *testing.T) {
	for _, mutation := range []string{"branch_generation", "source_revision"} {
		t.Run(mutation, func(t *testing.T) {
			captured := captureTranscriptWebFullProjection(t)
			if mutation == "source_revision" {
				mutateTranscriptWebFullProjectionSource(t, captured.fixture)
			} else {
				switchTranscriptWebFullProjectionBranch(t, captured)
			}
			before := requireTranscriptWebReadModelFence(t, captured.fixture.readModel, captured.fixture.stream, captured.snapshot.BranchID)
			if mutation == "source_revision" && before.SourceRevision <= captured.fence.SourceRevision ||
				mutation == "branch_generation" && before.BranchGeneration <= captured.fence.BranchGeneration {
				t.Fatal("countercase did not change its canonical fence")
			}
			if err := captured.finish(t); !errors.Is(err, transcriptstore.ErrBranchStateStale) {
				t.Fatalf("changed prefix was accepted: %v", err)
			}
			assertTranscriptWebFullProjectionFenceUnchanged(t, captured.fixture, before)
		})
	}
}

func TestTranscriptWebFullProjectionRejectsLateProjectionOwner(t *testing.T) {
	captured := captureTranscriptWebFullProjection(t)
	if err := captured.fixture.server.rebuildTranscriptWebReadModel(t.Context(), captured.work); err != nil {
		t.Fatal(err)
	}
	before := requireTranscriptWebReadModelFence(t, captured.fixture.readModel, captured.fixture.stream, captured.snapshot.BranchID)
	if before.StateProjectionRevision <= captured.fence.StateProjectionRevision || !transcriptWebFenceReady(before) {
		t.Fatal("the winning full owner did not publish its current projection")
	}
	if err := captured.finish(t); !errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
		t.Fatalf("late full owner adopted a newer projection CAS: %v", err)
	}
	assertTranscriptWebFullProjectionFenceUnchanged(t, captured.fixture, before)
	assertPreparationProjectionMatchesCanonical(t, captured.fixture)
}

func TestTranscriptWebFullPrefixApplyRejectsChangedCanonicalSource(t *testing.T) {
	fixture, input := nextTranscriptWebFullProjectionApply(t)
	appendTranscriptWebFullProjectionTail(t, fixture, "atomic-source-tail")
	input.State.Status = "building"
	mutateTranscriptWebFullProjectionSource(t, fixture)
	before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, input.State.BranchID)
	if input.ExpectedCanonicalSourceRevision == nil || *input.ExpectedCanonicalSourceRevision != 0 ||
		before.SourceRevision <= *input.ExpectedCanonicalSourceRevision ||
		before.ThroughPublicationSequence <= input.State.ThroughPublicationSequence {
		t.Fatal("fixture did not retain a zero-revision prefix while canonical source advanced")
	}
	if err := fixture.readModel.ApplyTranscriptWebProjection(t.Context(), input); !errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
		t.Fatalf("atomic prefix precondition accepted changed canonical source: %v", err)
	}
	assertTranscriptWebFullProjectionFenceUnchanged(t, fixture, before)
}

func TestTranscriptWebFullPrefixApplyKeepsExactReadyHead(t *testing.T) {
	fixture, input := nextTranscriptWebFullProjectionApply(t)
	appendTranscriptWebFullProjectionTail(t, fixture, "late-ready-tail")
	before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, input.State.BranchID)
	if input.State.Status != "ready" || before.SourceRevision != input.State.SourceRevision ||
		before.ThroughPublicationSequence <= input.State.ThroughPublicationSequence {
		t.Fatal("fixture did not isolate a tail append after ready selection")
	}
	if err := fixture.readModel.ApplyTranscriptWebProjection(t.Context(), input); !errors.Is(err, transcriptstore.ErrTranscriptWebProjectionStale) {
		t.Fatalf("ready projection ignored an append racing Apply: %v", err)
	}
	assertTranscriptWebFullProjectionFenceUnchanged(t, fixture, before)
}

type transcriptWebCapturedFullProjection struct {
	fixture  transcriptWebReadModelServingFixture
	work     transcriptstore.TranscriptWebProjectionWork
	fence    transcriptstore.TranscriptWebProjectionFence
	snapshot transcriptstore.ProjectionSnapshot
	messages []map[string]any
}

func captureTranscriptWebFullProjection(t *testing.T) transcriptWebCapturedFullProjection {
	t.Helper()
	fixture := newTranscriptWebSharedFullRebuildFixture(t)
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, work.BranchID)
	snapshot := transcriptstore.ProjectionSnapshot{StreamUID: fence.StreamUID, BranchID: fence.BranchID,
		BranchGeneration: fence.BranchGeneration, ThroughPublicationSequence: fence.ThroughPublicationSequence}
	messages, err := fixture.server.projectTranscriptWebHistoryPrefix(
		t.Context(), fixture.stream, fixture.stream.OwnerID, fixture.stream.SessionID, snapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	return transcriptWebCapturedFullProjection{fixture: fixture, work: work, fence: fence, snapshot: snapshot, messages: messages}
}

func (captured transcriptWebCapturedFullProjection) finish(t *testing.T) error {
	t.Helper()
	return captured.fixture.server.finishTranscriptWebFullProjection(
		t.Context(), captured.fixture.stream, captured.work, captured.fence, captured.messages, captured.snapshot,
	)
}

func nextTranscriptWebFullProjectionApply(t *testing.T) (transcriptWebReadModelServingFixture, transcriptstore.ApplyTranscriptWebProjectionInput) {
	t.Helper()
	captured := captureTranscriptWebFullProjection(t)
	if err := captured.finish(t); err != nil {
		t.Fatal(err)
	}
	state, records, found, err := captured.fixture.readModel.GetTranscriptWebProjectionCheckpoint(
		t.Context(), captured.fixture.stream.OwnerID, captured.fixture.stream.UID, captured.snapshot.BranchID,
	)
	if err != nil || !found || state.Status != "ready" || state.MessageArtifactReferenceCount != 0 {
		t.Fatalf("full Apply fixture not ready: found=%t state=%#v err=%v", found, state, err)
	}
	fence := requireTranscriptWebReadModelFence(t, captured.fixture.readModel, captured.fixture.stream, captured.snapshot.BranchID)
	state.ProjectionRevision++
	return captured.fixture, transcriptstore.ApplyTranscriptWebProjectionInput{
		OwnerID: captured.fixture.stream.OwnerID, State: state, ReplaceAll: true, Messages: records,
		ExpectedBranchGeneration: fence.StateBranchGeneration, ExpectedThroughPublication: fence.StateThroughPublicationSequence,
		ExpectedProjectionRevision: fence.StateProjectionRevision, ExpectedSourceRevision: fence.StateSourceRevision,
		ExpectedSourceChainSHA256: fence.StateSourceChainSHA256, ExpectedCanonicalSourceRevision: &state.SourceRevision,
	}
}

func assertTranscriptWebFullProjectionFenceUnchanged(t *testing.T, fixture transcriptWebReadModelServingFixture, before transcriptstore.TranscriptWebProjectionFence) {
	t.Helper()
	after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, before.BranchID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected full projection mutated the canonical or stored projection fence")
	}
}

func mutateTranscriptWebFullProjectionSource(t *testing.T, fixture transcriptWebReadModelServingFixture) {
	t.Helper()
	result, err := fixture.db.ExecContext(t.Context(), `UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND client_message_id=?`,
		[]byte(`{"messageUuid":"synthetic-full-source","text":"changed source","role":"user","messageOrigin":"task_intent"}`),
		fixture.stream.UID, transcriptWebServingClientMessageID(0))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("source mutation rows=%d err=%v", count, err)
	}
}

func switchTranscriptWebFullProjectionBranch(t *testing.T, captured transcriptWebCapturedFullProjection) {
	t.Helper()
	fork, err := captured.fixture.repository.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{
		StreamUID: captured.fixture.stream.UID, OwnerID: captured.fixture.stream.OwnerID,
		SourceBranchID: captured.snapshot.BranchID, ExpectedActiveBranchID: captured.snapshot.BranchID,
		ExpectedGeneration: captured.snapshot.BranchGeneration, ClientMutationID: "full-projection-fork",
		SourceClientMessageID: transcriptWebServingClientMessageID(0), SourceMessageIndex: 0,
		ReplacementText: "synthetic replacement", Destinations: []string{transcriptWebDestination},
	})
	if err != nil || !fork.Created {
		t.Fatalf("fork created=%t err=%v", fork.Created, err)
	}
	returned, err := captured.fixture.repository.AppendFrameUserEventToBranch(t.Context(), transcriptstore.AppendFrameUserEventToBranchInput{
		AppendFrameUserEventInput: transcriptstore.AppendFrameUserEventInput{
			StreamUID: captured.fixture.stream.UID, OwnerID: captured.fixture.stream.OwnerID,
			ClientMessageID: "branch-continue:full-projection-return", MessageUUID: "branch-continue:full-projection-return",
			FrameEventID: "frame-branch-continue:" + captured.fixture.stream.FrameID + ":" + captured.snapshot.BranchID + ":full-projection-return",
			Text:         "return to original synthetic branch", Destinations: []string{transcriptWebDestination},
		},
		TargetBranchID: captured.snapshot.BranchID, ExpectedActiveBranchID: fork.BranchID,
		ExpectedGeneration: fork.Generation, ClientMutationID: "full-projection-return",
	})
	if err != nil || !returned.Created || !returned.Switched {
		t.Fatalf("return created=%t switched=%t err=%v", returned.Created, returned.Switched, err)
	}
}

func appendTranscriptWebFullProjectionTail(t *testing.T, fixture transcriptWebReadModelServingFixture, suffix string) {
	t.Helper()
	_, _, created, err := fixture.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
		ClientMessageID: "full-projection-client-" + suffix, FrameEventID: "full-projection-event-" + suffix,
		MessageUUID: "full-projection-message-" + suffix, Text: "ordinary synthetic tail " + suffix,
		Destinations: []string{transcriptWebDestination},
	})
	if err != nil || !created {
		t.Fatalf("append created=%t err=%v", created, err)
	}
}
