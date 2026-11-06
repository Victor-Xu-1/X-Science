package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTranscriptWebRequestRepairSettlesDeterministicFailureOnce(t *testing.T) {
	for _, owner := range []string{"request", "background"} {
		t.Run(owner, func(t *testing.T) {
			fixture := newTranscriptWebReadModelServingFixture(t, 1)
			before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
			// Reuse the stable-source conflict from the background quarantine
			// contract. All source and retained display rows belong to this Store.
			corruptTranscriptWebServingSource(t, fixture)
			if owner == "background" {
				if err := fixture.server.runTranscriptWebReadModelCycle(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

			responses := make([]*httptest.ResponseRecorder, 2)
			for index := range responses {
				responses[index] = p3JSONRequest(t, fixture.server, http.MethodGet,
					"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
				clock := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
				work, found, err := fixture.readModel.GetTranscriptWebProjectionWork(context.Background(),
					fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("owner=%s GET=%d status=%d projection=%#v pending_work=%t work_status=%s reason=%s",
					owner, index+1, responses[index].Code, clock, found, work.Status, work.Reason)
				fixture.server.transcriptWebCatchupMu.Lock()
				active := len(fixture.server.transcriptWebCatchups)
				fixture.server.transcriptWebCatchupMu.Unlock()
				if active != 0 {
					t.Fatalf("settled GET retained %d projection owners", active)
				}
			}

			after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fixture.fence.BranchID)
			if after.status != "quarantined" || after.lastErrorCode != "projection_source_conflict" ||
				after.revision != before.revision+1 {
				t.Fatalf("unchanged deterministic failure did not settle once: before=%#v after=%#v", before, after)
			}
			fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if fence.StateSourceRevision != fence.SourceRevision ||
				fence.StateThroughPublicationSequence != fence.ThroughPublicationSequence || transcriptWebFenceReady(fence) {
				t.Fatalf("quarantine lost its source boundary or claimed fresh history: %#v", fence)
			}
			for _, response := range responses {
				requireTranscriptWebServingPage(t, response, 1, 0, 0)
			}
		})
	}
}

func TestTranscriptWebRequestRepairWithoutVerifiedRowsStaysUnavailable(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	if _, err := fixture.db.Exec(`DELETE FROM transcript_web_projection_state
		WHERE stream_uid=? AND branch_id=?`, fixture.stream.UID, fixture.fence.BranchID); err != nil {
		t.Fatal(err)
	}
	corruptTranscriptWebServingSource(t, fixture)
	for range 2 {
		response := p3JSONRequest(t, fixture.server, http.MethodGet,
			"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
		assertTranscriptWebServingUnavailable(t, response, "message")
		fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
		if fence.StateMessageCount != 0 || fence.VisibleMessageCount != 0 ||
			transcriptWebFenceReady(fence) || transcriptWebVerifiedSnapshotEligible(fence) {
			t.Fatalf("unverified source invented display rows or fresh history: %#v", fence)
		}
	}
}

func corruptTranscriptWebServingSource(t *testing.T, fixture transcriptWebReadModelServingFixture) {
	t.Helper()
	result, err := fixture.db.Exec(`UPDATE transcript_events SET payload_json=?
		WHERE stream_uid=? AND client_message_id=?`, []byte(`{"invalid"`),
		fixture.stream.UID, transcriptWebServingClientMessageID(0))
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("corrupt synthetic source affected=%d err=%v", affected, err)
	}
}
