package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebIncrementalPreparationStageMatchesFullProjection(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := map[bool]string{false: "user_only", true: "mixed_assistant"}[mixed]
		t.Run(name, func(t *testing.T) {
			fixture := newTranscriptWebReadModelServingFixture(t, 1)
			before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if _, _, created, err := fixture.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
				ClientMessageID: "preparation-next-input", FrameEventID: "preparation-next-frame-event",
				MessageUUID: "preparation-next-message", Text: "next ordinary task",
			}); err != nil || !created {
				t.Fatalf("append next input created=%t err=%v", created, err)
			}
			claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
				RunnerID: "preparation-stage-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claim.Claim}}
			if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
				t.Fatal(err)
			}
			if err := fixture.server.checkpointSessionRunnerPreparationStage(t.Context(), run, "history_replay"); err != nil {
				t.Fatal(err)
			}
			wantWritten := 1
			if mixed {
				if _, err := fixture.server.appendTranscriptRunnerEvent(t.Context(), run.Transcript,
					"content_delta", "preparation-next-response", map[string]any{"text": "ordinary response"}); err != nil {
					t.Fatal(err)
				}
				wantWritten++
			}
			work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
			fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if work.DirtyFirstAffectedOrdinal != 0 || work.DirtyReasonMask != 0 ||
				fence.StateSourceRevision != fence.SourceRevision || fence.StateBranchGeneration != fence.BranchGeneration {
				t.Fatalf("fixture must reach event classification without bypassing dirty/source/branch guards: work=%#v fence=%#v", work, fence)
			}
			result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
			t.Logf("actual preparation producer: disposition=%s events=%d messages=%d err=%v",
				result.Disposition, result.EventsRead, result.MessagesWritten, err)
			if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady || result.MessagesWritten != wantWritten {
				t.Fatalf("nonvisual preparation forced full history: result=%#v err=%v", result, err)
			}
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if !transcriptWebFenceReady(after) || after.StateMessageCount != before.StateMessageCount+wantWritten ||
				after.StateThroughPublicationSequence != work.ThroughPublicationSequence ||
				after.StateSourceChainSHA256 == before.StateSourceChainSHA256 {
				t.Fatalf("incremental source or public rows mismatch: before=%#v after=%#v", before, after)
			}
			assertPreparationProjectionMatchesCanonical(t, fixture)
		})
	}
}

func assertPreparationProjectionMatchesCanonical(t *testing.T, fixture transcriptWebReadModelServingFixture) {
	t.Helper()
	state, records, found, err := fixture.readModel.GetTranscriptWebProjectionCheckpoint(
		t.Context(), fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID,
	)
	if err != nil || !found {
		t.Fatalf("read projection found=%t err=%v", found, err)
	}
	messages, snapshot, err := fixture.server.projectTranscriptWebHistory(
		t.Context(), fixture.stream, fixture.stream.OwnerID, fixture.stream.SessionID, fixture.fence.BranchID,
	)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := fixture.server.projectTranscriptWebSourceMetadata(t.Context(), fixture.stream,
		fixture.stream.OwnerID, fixture.stream.SessionID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	canonical, references, err := buildTranscriptWebMessageRecords(messages, metadata)
	if err != nil || len(records) != len(canonical) || state.MessageCount != len(canonical) ||
		state.VisibleMessageCount != len(canonical) || state.MessageArtifactReferenceCount != len(references) ||
		state.SourceChainSHA256 != metadata.eventChain || state.ThroughPublicationSequence != snapshot.ThroughPublicationSequence {
		t.Fatalf("full/incremental counts or source boundary differ: rows=%d/%d err=%v", len(records), len(canonical), err)
	}
	for index := range records {
		// Full records derive this bookkeeping timestamp from the public
		// created_at milliseconds; assistant increments retain event nanoseconds.
		// Require the same public time and compare every remaining row field,
		// including exact JSON/SHA, identities, coordinates and artifact refs.
		if records[index].UpdatedAt.UnixMilli() != canonical[index].UpdatedAt.UnixMilli() {
			t.Fatalf("full/incremental public time differs at row %d", index)
		}
		records[index].UpdatedAt = time.UnixMilli(records[index].UpdatedAt.UnixMilli()).UTC()
		canonical[index].UpdatedAt = time.UnixMilli(canonical[index].UpdatedAt.UnixMilli()).UTC()
	}
	if !reflect.DeepEqual(records, canonical) {
		t.Fatal("full/incremental public rows, identities, coordinates or artifact references differ")
	}
	var checkpoint transcriptWebProjectorCheckpointV1
	if err := json.Unmarshal(state.ProjectorStateJSON, &checkpoint); err != nil ||
		checkpoint.MessageCount != state.MessageCount || checkpoint.EventChainSHA256 != metadata.eventChain {
		t.Fatalf("checkpoint lost canonical count/source binding: err=%v", err)
	}
}

func TestTranscriptWebIncrementalPreparationStageRetainsSourceInvalidation(t *testing.T) {
	fixture, claim := newTranscriptWebRecoveryBoundaryFixture(t)
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claim}}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.checkpointSessionRunnerPreparationStage(t.Context(), run, "history_replay"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND client_message_id=?`,
		[]byte(`{"text":"changed canonical source"}`), fixture.stream.UID, transcriptWebServingClientMessageID(0)); err != nil {
		t.Fatal(err)
	}
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
	if work.DirtyReasonMask == 0 || work.DirtyFirstAffectedOrdinal == 0 {
		t.Fatalf("real source update did not mark the projection dirty: %#v", work)
	}
	result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
	if err != nil || result.Disposition != transcriptWebIncrementalFallbackDirty || result.EventsRead != 0 {
		t.Fatalf("nonvisual metadata bypassed source invalidation: result=%#v err=%v", result, err)
	}
	if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID); after != before {
		t.Fatalf("dirty fallback mutated projection before=%#v after=%#v", before, after)
	}
}

func TestTranscriptWebIncrementalPreparationMetadataRemainsClosed(t *testing.T) {
	for _, test := range []struct{ stage, phase string }{
		{"history_replay", "context"}, {"model_resolution", "recovery"},
		{"prompt_snapshot", "snapshot"}, {"model_execution", "provider"},
	} {
		t.Run(test.stage, func(t *testing.T) {
			projected := preparationMetadataProjection(t, map[string]any{
				"status": "running", "stage": test.stage,
				"detail": sessionRunnerPreparationStageDetail(test.stage), "lifecyclePhase": test.phase,
			})
			if !transcriptWebIncrementalMetadataCheckpoint(projected) {
				t.Fatalf("typed producer stage was not metadata-only: %#v", test)
			}
		})
	}
	for _, kind := range []string{
		"unknown_stage", "resume_boundary", "extra_tool", "extra_content", "wrong_detail", "wrong_phase",
		"untrimmed_stage", "terminal_status", "foreign_identity", "legacy_source", "missing_attempt", "frame_ref", "private_snapshot",
	} {
		t.Run(kind, func(t *testing.T) {
			payload := map[string]any{"status": "running", "stage": "history_replay",
				"detail": sessionRunnerPreparationStageDetail("history_replay"), "lifecyclePhase": "context"}
			switch kind {
			case "unknown_stage":
				payload["stage"], payload["detail"] = "unknown", sessionRunnerPreparationStageDetail("unknown")
			case "resume_boundary":
				payload["stage"], payload["detail"], payload["lifecyclePhase"] = "resume_state", sessionRunnerPreparationStageDetail("resume_state"), "recovery"
			case "extra_tool":
				payload["toolCallId"] = "real-boundary"
			case "extra_content":
				payload["text"] = "visible content"
			case "wrong_detail":
				payload["detail"] = "different semantics"
			case "wrong_phase":
				payload["lifecyclePhase"] = "tool"
			case "untrimmed_stage":
				payload["stage"] = " history_replay "
			case "terminal_status":
				payload["status"] = "completed"
			case "private_snapshot":
				payload = map[string]any{"kind": transcriptstore.RunnerTaskMemorySnapshotKind}
			}
			projected := preparationMetadataProjection(t, payload)
			switch kind {
			case "foreign_identity":
				projected.Event.ClientMessageID = "other-checkpoint"
			case "legacy_source":
				projected.Event.Source = transcriptstore.EventSourceFrameRef
			case "missing_attempt":
				projected.Event.RunnerAttempt = nil
			case "frame_ref":
				frameEventID := "legacy-reference"
				projected.Event.FrameEventID = &frameEventID
			}
			if transcriptWebIncrementalMetadataCheckpoint(projected) ||
				classifyTranscriptWebIncrementalEvent(projected) != transcriptWebIncrementalFallbackTool {
				t.Fatalf("unsupported or boundary-affecting checkpoint escaped full replay: %s", kind)
			}
		})
	}
}

func preparationMetadataProjection(t *testing.T, payload map[string]any) transcriptstore.ProjectedEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	attempt := int64(1)
	return transcriptstore.ProjectedEvent{Event: transcriptstore.Event{
		Type: "runner_checkpoint", Source: transcriptstore.EventSourcePayload,
		RunnerAttempt: &attempt, ClientMessageID: "transcript-runner:" + strings.Repeat("a", 64),
	}, ResolvedPayloadJSON: raw}
}
