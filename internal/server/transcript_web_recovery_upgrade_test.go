package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebRecoveryBoundaryProjectorUpgradeRebuildsPhantomRows(t *testing.T) {
	for _, path := range []string{"background", "public_read"} {
		t.Run(path, func(t *testing.T) {
			fixture, claim := newTranscriptWebRecoveryBoundaryFixture(t)
			state, _, found, err := fixture.readModel.GetTranscriptWebProjectionCheckpoint(
				t.Context(), fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID,
			)
			if err != nil || !found {
				t.Fatalf("initial projection: found=%t err=%v", found, err)
			}
			appendRunnerToolCheckpoint(t, fixture.repository, claim, "old-recovery-boundary", recoveryBoundaryPayload())
			work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
			page, err := fixture.repository.ListProjectedEvents(t.Context(), transcriptstore.ListProjectedEventsInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, BranchID: work.BranchID,
				BranchGeneration: work.BranchGeneration, AfterPublicationSequence: state.ThroughPublicationSequence,
				ThroughPublicationSequence: work.ThroughPublicationSequence, Limit: transcriptWebIncrementalBatch,
			})
			if err != nil || len(page) != 1 || !transcriptWebIncrementalProviderBoundary(page[0]) {
				t.Fatalf("legacy boundary fixture: page=%+v err=%v", page, err)
			}
			// Materialize the precise old bad row through the original row builder
			// and normal derived-store fence, never by editing canonical events.
			phantom, _, err := buildIncrementalTranscriptWebUserMessage(page[0], fixture.stream.SessionID,
				work.BranchID, state.MessageCount, state.VisibleMessageCount)
			if err != nil {
				t.Fatal(err)
			}
			next := state
			next.ProjectionRevision++
			next.ThroughPublicationSequence = work.ThroughPublicationSequence
			next.SourceRevision = work.SourceRevision
			next.MessageCount++
			next.VisibleMessageCount++
			next.UpdatedAt = time.Now().UTC()
			next.SourceChainSHA256, err = extendTranscriptWebSourceEventChain(state.SourceChainSHA256, page)
			if err != nil {
				t.Fatal(err)
			}
			var checkpoint transcriptWebProjectorCheckpointV1
			if err := json.Unmarshal(state.ProjectorStateJSON, &checkpoint); err != nil {
				t.Fatal(err)
			}
			checkpoint.Mode, checkpoint.ThroughPublicationSequence = "incremental_append", next.ThroughPublicationSequence
			checkpoint.SourceRevision, checkpoint.MessageCount = next.SourceRevision, next.MessageCount
			checkpoint.EventChainSHA256 = next.SourceChainSHA256
			next.ProjectorStateJSON, err = json.Marshal(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			next.ProjectorStateSHA256 = transcriptstore.TranscriptWebSHA256(next.ProjectorStateJSON)
			if err := fixture.readModel.ApplyTranscriptWebProjection(t.Context(), transcriptstore.ApplyTranscriptWebProjectionInput{
				OwnerID: work.OwnerID, State: next, ExpectedBranchGeneration: state.BranchGeneration,
				ExpectedThroughPublication: state.ThroughPublicationSequence, ExpectedProjectionRevision: state.ProjectionRevision,
				ExpectedSourceRevision: state.SourceRevision, ExpectedSourceChainSHA256: state.SourceChainSHA256,
				Messages: []transcriptstore.TranscriptWebMessageRecord{phantom},
			}); err != nil {
				t.Fatal(err)
			}
			// Only the isolated derived-view version marker models the previous
			// deployed reducer. This is the repository's existing upgrade fixture.
			if _, err := fixture.db.Exec(`UPDATE transcript_web_projection_state SET projector_version=14 WHERE stream_uid=? AND branch_id=?`,
				fixture.stream.UID, fixture.fence.BranchID); err != nil {
				t.Fatal(err)
			}
			upgrade, found, err := fixture.readModel.GetTranscriptWebProjectionWork(t.Context(), work.OwnerID, work.StreamUID, work.BranchID)
			if err != nil || !found || upgrade.Reason != "projection_projector_version_stale" {
				t.Fatalf("existing phantom view was not selected for the original versioned rebuild: work=%+v found=%t err=%v", upgrade, found, err)
			}
			if path == "background" {
				if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
					t.Fatal(err)
				}
			} else {
				response := p3JSONRequest(t, fixture.server, http.MethodGet,
					"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
				requireTranscriptWebServingPage(t, response, 1, 0, 0)
			}
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if !transcriptWebFenceReady(after) || after.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion ||
				after.StateMessageCount != 1 || after.StateProjectionRevision <= next.ProjectionRevision ||
				after.StateSourceChainSHA256 != next.SourceChainSHA256 || after.SourceRevision != next.SourceRevision {
				t.Fatalf("versioned rebuild did not retire only the old derived row: after=%+v", after)
			}
			assertTranscriptWebRecoveryMatchesCanonical(t, fixture)
			unchanged, err := fixture.repository.ListProjectedEvents(t.Context(), transcriptstore.ListProjectedEventsInput{
				StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, BranchID: work.BranchID,
				BranchGeneration: work.BranchGeneration, AfterPublicationSequence: state.ThroughPublicationSequence,
				ThroughPublicationSequence: work.ThroughPublicationSequence, Limit: transcriptWebIncrementalBatch,
			})
			if err != nil || !reflect.DeepEqual(unchanged, page) {
				t.Fatalf("derived repair changed canonical recovery events: err=%v", err)
			}
			if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			stable := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if stable.StateProjectionRevision != after.StateProjectionRevision {
				t.Fatal("settled version repair ran again without a source change")
			}
		})
	}
}
