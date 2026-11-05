package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebHistoryReadDoesNotRebuildInvalidBuildingCheckpoint(t *testing.T) {
	fixture := newTranscriptWebBuildingCatchupFixture(t, transcriptWebIncrementalBatch+1)
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	var checkpoint transcriptWebProjectorCheckpointV1
	if err := json.Unmarshal(fence.StateProjectorStateJSON, &checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.Version--
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE transcript_web_projection_state
		SET projector_state_json=?,projector_state_sha256=? WHERE stream_uid=? AND branch_id=?`,
		string(raw), transcriptstore.TranscriptWebSHA256(raw), fixture.stream.UID, fixture.fence.BranchID); err != nil {
		t.Fatal(err)
	}
	before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
	response := p3JSONRequest(t, fixture.server, http.MethodGet,
		"/api/conversations/"+transcriptWebServingFrameID+"/messages?limit=1", nil, "")
	assertTranscriptWebServingUnavailable(t, response, "message")
	if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID); after != before {
		t.Fatalf("invalid building checkpoint was rewritten by a read: before=%#v after=%#v", before, after)
	}
}

func TestTranscriptWebHistoryReadsResumeValidBuildingProjection(t *testing.T) {
	for _, detail := range []bool{false, true} {
		t.Run(fmt.Sprintf("detail=%t", detail), func(t *testing.T) {
			fixture := newTranscriptWebBuildingCatchupFixture(t, transcriptWebIncrementalBatch+1)
			before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			identity := fmt.Sprintf("building-read-%06d", before.ThroughPublicationSequence)
			path := "/api/conversations/" + transcriptWebServingFrameID + "/messages"
			if detail {
				path += "/" + identity
			} else {
				path += "?limit=1"
			}
			response := p3JSONRequest(t, fixture.server, http.MethodGet, path, nil, "")
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			t.Logf("HTTP%d projection=%s through=%d -> %s through=%d source=%d",
				response.Code, before.StateStatus, before.StateThroughPublicationSequence,
				after.StateStatus, after.StateThroughPublicationSequence, after.SourceRevision)
			if response.Code != http.StatusOK {
				t.Fatalf("resumable building history remained unavailable: status=%d body=%s", response.Code, response.Body.String())
			}
			payload := p3DecodeObject(t, response)
			if !detail {
				items := transcriptWebServingItems(t, payload, "items")
				if len(items) != 1 {
					t.Fatalf("tail page contains %d items, want one", len(items))
				}
				payload = items[0].(map[string]any)
			}
			if webString(payload["id"]) != identity || !transcriptWebFenceReady(after) ||
				after.StateMessageCount != before.StateMessageCount+1 {
				t.Fatalf("request did not resume the exact final chunk: identity=%q fence=%#v", webString(payload["id"]), after)
			}
		})
	}
}

func TestTranscriptWebHistoryBuildingCatchupBudgetRemainsRetryable(t *testing.T) {
	fixture := newTranscriptWebBuildingCatchupFixture(t, 2*transcriptWebIncrementalBatch+1)
	before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	path := "/api/conversations/" + transcriptWebServingFrameID + "/messages?limit=1"
	first := p3JSONRequest(t, fixture.server, http.MethodGet, path, nil, "")
	assertTranscriptWebServingUnavailable(t, first, "message")
	partial := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if partial.StateStatus != "building" ||
		partial.StateThroughPublicationSequence != before.StateThroughPublicationSequence+transcriptWebIncrementalBatch ||
		partial.StateMessageCount != before.StateMessageCount+transcriptWebIncrementalBatch ||
		partial.ThroughPublicationSequence != before.ThroughPublicationSequence {
		t.Fatalf("one request must advance exactly one bounded chunk: before=%#v after=%#v", before, partial)
	}
	second := p3JSONRequest(t, fixture.server, http.MethodGet, path, nil, "")
	after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if second.Code != http.StatusOK || !transcriptWebFenceReady(after) ||
		after.StateMessageCount != partial.StateMessageCount+1 {
		t.Fatalf("next request did not resume the retained checkpoint: status=%d fence=%#v", second.Code, after)
	}
	items := transcriptWebServingItems(t, p3DecodeObject(t, second), "items")
	identity := fmt.Sprintf("building-read-%06d", before.ThroughPublicationSequence)
	if len(items) != 1 || webString(items[0].(map[string]any)["id"]) != identity {
		t.Fatalf("retry did not serve the exact final message: items=%#v", items)
	}
	t.Logf("one request HTTP%d building through=%d -> %d; retry HTTP%d ready through=%d",
		first.Code, before.StateThroughPublicationSequence, partial.StateThroughPublicationSequence,
		second.Code, after.StateThroughPublicationSequence)
}

func newTranscriptWebBuildingCatchupFixture(t *testing.T, events int) transcriptWebReadModelServingFixture {
	t.Helper()
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	// Reuse the durable event/membership fixture, then produce building through
	// the real bounded projector. No raw status toggle or background worker.
	seedTranscriptWebIncrementalUserEvents(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID,
		events, "building-read")
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	first, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
	if err != nil || first.Disposition != transcriptWebIncrementalAppliedBuilding || first.EventsRead != transcriptWebIncrementalBatch {
		t.Fatalf("first bounded projection=%#v err=%v", first, err)
	}
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if fence.StateStatus != "building" || fence.StateThroughPublicationSequence >= fence.ThroughPublicationSequence ||
		fence.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion {
		t.Fatalf("expected a current resumable building prefix: %#v", fence)
	}
	return fixture
}
