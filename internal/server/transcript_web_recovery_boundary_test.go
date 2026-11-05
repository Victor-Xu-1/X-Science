package server

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebIsolatedRecoveryBoundaryDoesNotBecomeUserMessage(t *testing.T) {
	for _, order := range []string{"isolated", "before_user", "after_user"} {
		t.Run(order, func(t *testing.T) {
			fixture, claim := newTranscriptWebRecoveryBoundaryFixture(t)
			before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			appendUser := func() {
				if _, _, created, err := fixture.repository.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
					StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID,
					ClientMessageID: "boundary-next-user", FrameEventID: "boundary-next-frame-event",
					MessageUUID: "boundary-next-message", Text: "real next user input",
				}); err != nil || !created {
					t.Fatalf("append actual user event: created=%t err=%v", created, err)
				}
			}
			if order == "after_user" {
				appendUser()
			}
			appendRunnerToolCheckpoint(t, fixture.repository, claim, "isolated-recovery-boundary", recoveryBoundaryPayload())
			if order == "before_user" {
				appendUser()
			}
			work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
			result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
			if err != nil {
				t.Fatal(err)
			}
			wantWritten := 0
			if order != "isolated" {
				wantWritten = 1
			}
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if result.Disposition != transcriptWebIncrementalAppliedReady || result.MessagesWritten != wantWritten ||
				after.StateMessageCount != before.StateMessageCount+wantWritten ||
				after.StateThroughPublicationSequence != work.ThroughPublicationSequence ||
				after.StateSourceChainSHA256 == before.StateSourceChainSHA256 {
				t.Fatalf("metadata boundary changed public rows or lost source progress: result=%+v before=%+v after=%+v", result, before, after)
			}
			assertTranscriptWebRecoveryMatchesCanonical(t, fixture)
		})
	}
}

func TestTranscriptWebMalformedRecoveryBoundaryRetainsToolFallback(t *testing.T) {
	for _, kind := range []string{"unknown_reason", "non_metadata_payload", "invalid_continuation"} {
		t.Run(kind, func(t *testing.T) {
			fixture, claim := newTranscriptWebRecoveryBoundaryFixture(t)
			before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
			payload := recoveryBoundaryPayload()
			switch kind {
			case "unknown_reason":
				payload["reason_code"] = "unrecognized-synthetic-reason"
			case "non_metadata_payload":
				payload["tool_call"] = map[string]any{"name": "synthetic-tool"}
			case "invalid_continuation":
				payload = map[string]any{"status": "running", "provider_continuation": map[string]any{"contract_version": -1}}
			}
			appendRunnerToolCheckpoint(t, fixture.repository, claim, "malformed-recovery-boundary", payload)
			work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
			result, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
			if err != nil || result.Disposition != transcriptWebIncrementalFallbackTool || result.MessagesWritten != 0 {
				t.Fatalf("unsupported checkpoint escaped typed fallback: result=%+v err=%v", result, err)
			}
			if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID); after != before {
				t.Fatalf("fallback changed derived rows before canonical rebuild: before=%+v after=%+v", before, after)
			}
		})
	}
}

func newTranscriptWebRecoveryBoundaryFixture(t *testing.T) (transcriptWebReadModelServingFixture, transcriptstore.RunnerClaim) {
	t.Helper()
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "isolated-recovery-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim synthetic runner: claimed=%t err=%v", claim.Claimed, err)
	}
	if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	return fixture, claim.Claim
}

func recoveryBoundaryPayload() map[string]any {
	return map[string]any{"status": "interrupted", "reason_code": sessionRunnerProviderTransportTemporaryReasonCode,
		"resume_detail": "synthetic recoverable transport boundary"}
}

func assertTranscriptWebRecoveryMatchesCanonical(t *testing.T, fixture transcriptWebReadModelServingFixture) {
	t.Helper()
	state, records, found, err := fixture.readModel.GetTranscriptWebProjectionCheckpoint(
		t.Context(), fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID,
	)
	if err != nil || !found {
		t.Fatalf("read projected checkpoint: found=%t err=%v", found, err)
	}
	full, snapshot, err := fixture.server.projectTranscriptWebHistory(
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
	canonical, references, err := buildTranscriptWebMessageRecords(full, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if state.MessageCount != len(canonical) || state.VisibleMessageCount != len(canonical) ||
		state.MessageArtifactReferenceCount != len(references) || state.SourceChainSHA256 != metadata.eventChain ||
		!reflect.DeepEqual(records, canonical) {
		t.Fatalf("incremental public rows/counts/source chain differ from canonical projection: state=%+v records=%+v canonical=%+v", state, records, canonical)
	}
	var checkpoint transcriptWebProjectorCheckpointV1
	if err := json.Unmarshal(state.ProjectorStateJSON, &checkpoint); err != nil ||
		checkpoint.MessageCount != state.MessageCount || checkpoint.EventChainSHA256 != state.SourceChainSHA256 ||
		checkpoint.ThroughPublicationSequence != snapshot.ThroughPublicationSequence {
		t.Fatalf("durable checkpoint lost canonical source/count binding: checkpoint=%+v err=%v", checkpoint, err)
	}
}
