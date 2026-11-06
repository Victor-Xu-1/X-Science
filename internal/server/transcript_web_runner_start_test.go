package server

import (
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebIncrementalTypedRunnerStartMatchesFullProjection(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := map[bool]string{false: "user_only", true: "mixed_assistant"}[mixed]
		t.Run(name, func(t *testing.T) {
			fixture := newTranscriptWebReadModelServingFixture(t, 1)
			before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if _, _, created, err := fixture.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
				ClientMessageID: "runner-start-next-input", FrameEventID: "runner-start-next-frame-event",
				MessageUUID: "runner-start-next-message", Text: "next ordinary task",
			}); err != nil || !created {
				t.Fatalf("append next input created=%t err=%v", created, err)
			}
			claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
				RunnerID: "typed-start-runner", TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			authority := &transcriptRunnerAuthority{Stream: fixture.stream, Claim: claim.Claim}
			if err := fixture.server.checkpointRunnerStarted(t.Context(), authority); err != nil {
				t.Fatal(err)
			}
			wantWritten := 1
			if mixed {
				if _, err := fixture.server.appendTranscriptRunnerEvent(t.Context(), authority,
					"content_delta", "runner-start-next-response", map[string]any{"text": "ordinary response"}); err != nil {
					t.Fatal(err)
				}
				wantWritten++
			}
			work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
			fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if work.DirtyFirstAffectedOrdinal != 0 || work.DirtyReasonMask != 0 ||
				fence.StateSourceRevision != fence.SourceRevision || fence.StateBranchGeneration != fence.BranchGeneration {
				t.Fatalf("fixture did not reach classification through unchanged source guards: work=%#v fence=%#v", work, fence)
			}
			result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
			t.Logf("actual typed start producer: disposition=%s events=%d messages=%d err=%v",
				result.Disposition, result.EventsRead, result.MessagesWritten, err)
			if err != nil || result.Disposition != transcriptWebIncrementalAppliedReady || result.MessagesWritten != wantWritten {
				t.Fatalf("typed nonvisual start forced full history: result=%#v err=%v", result, err)
			}
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if !transcriptWebFenceReady(after) || after.StateMessageCount != before.StateMessageCount+wantWritten ||
				after.StateThroughPublicationSequence != work.ThroughPublicationSequence ||
				after.StateSourceChainSHA256 == before.StateSourceChainSHA256 {
				t.Fatalf("incremental source or display rows differ: before=%#v after=%#v", before, after)
			}
			assertPreparationProjectionMatchesCanonical(t, fixture)
		})
	}
}

func TestTranscriptWebIncrementalRunnerStartMarkerRemainsClosed(t *testing.T) {
	for _, kind := range []string{
		"known", "presentation_detail", "legacy_unmarked", "unknown_marker", "wrong_status", "missing_detail", "nonstring_detail",
		"extra_tool", "extra_content", "foreign_identity", "legacy_source", "missing_attempt", "nonpositive_attempt", "frame_reference", "artifact_reference", "resume_state", "task_memory",
	} {
		t.Run(kind, func(t *testing.T) {
			payload := map[string]any{"kind": runnerStartedCheckpointKind, "status": "running", "detail": "runner chat started"}
			switch kind {
			case "presentation_detail":
				payload["detail"] = "presentation text can change"
			case "legacy_unmarked":
				delete(payload, "kind")
			case "unknown_marker":
				payload["kind"] = "unknown_lifecycle_kind"
			case "wrong_status":
				payload["status"] = "completed"
			case "missing_detail":
				delete(payload, "detail")
			case "nonstring_detail":
				payload["detail"] = false
			case "extra_tool":
				payload["toolCallId"] = "actual-tool-boundary"
			case "extra_content":
				payload["text"] = "visible content"
			case "resume_state":
				payload = map[string]any{"status": "running", "stage": "resume_state",
					"detail": sessionRunnerPreparationStageDetail("resume_state"), "lifecyclePhase": "recovery"}
			case "task_memory":
				payload["kind"] = transcriptstore.RunnerTaskMemorySnapshotKind
			}
			projected := preparationMetadataProjection(t, payload)
			switch kind {
			case "foreign_identity":
				projected.Event.ClientMessageID = "external-checkpoint"
			case "legacy_source":
				projected.Event.Source = transcriptstore.EventSourceFrameRef
			case "missing_attempt":
				projected.Event.RunnerAttempt = nil
			case "nonpositive_attempt":
				attempt := int64(0)
				projected.Event.RunnerAttempt = &attempt
			case "frame_reference":
				frame := "legacy-frame-reference"
				projected.Event.FrameEventID = &frame
			case "artifact_reference":
				projected.ArtifactReferences = []transcriptstore.ArtifactReference{{}}
			}
			want := kind == "known" || kind == "presentation_detail"
			if got := transcriptWebIncrementalMetadataCheckpoint(projected); got != want {
				t.Fatalf("marker closure kind=%s got=%t want=%t", kind, got, want)
			}
			if !want && classifyTranscriptWebIncrementalEvent(projected) != transcriptWebIncrementalFallbackTool {
				t.Fatalf("unsupported checkpoint escaped full replay: kind=%s", kind)
			}
		})
	}
}
