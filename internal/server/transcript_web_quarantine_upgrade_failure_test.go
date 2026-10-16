package server

import (
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebProjectorUpgradeFailureSettlesWithoutOldSnapshotOrRetryLoop(t *testing.T) {
	for _, path := range []string{"public_read", "background"} {
		t.Run(path, func(t *testing.T) {
			fixture := newTranscriptWebReadModelServingFixture(t, 3)
			if _, err := fixture.db.Exec(`UPDATE transcript_web_projection_state SET
				projector_version=13,status='quarantined',last_error_code='projection_build_failed'
				WHERE stream_uid=? AND branch_id=?`, fixture.stream.UID, fixture.fence.BranchID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.db.Exec(`UPDATE transcript_events SET payload_json=?
				WHERE stream_uid=? AND event_id=(SELECT MIN(event_id) FROM transcript_events WHERE stream_uid=?)`,
				[]byte(`{"invalid"`), fixture.stream.UID, fixture.stream.UID); err != nil {
				t.Fatal(err)
			}
			if changed, err := fixture.store.TransitionCompatibilityFrameStatus(transcriptWebServingFrameID, "processing", "failed"); err != nil || !changed {
				t.Fatalf("terminal fixture changed=%t err=%v", changed, err)
			}
			before := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			var sourceEventsBefore int
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=?`, fixture.stream.UID).Scan(&sourceEventsBefore); err != nil {
				t.Fatal(err)
			}
			if path == "background" {
				if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
					t.Fatal(err)
				}
			}
			response := p3JSONRequest(t, fixture.server, http.MethodGet,
				"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
			assertTranscriptWebServingUnavailable(t, response, "message")
			after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if after.StateStatus != "quarantined" || after.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion ||
				after.StateProjectionRevision != before.StateProjectionRevision+1 || after.StateMessageCount != 0 ||
				after.VisibleMessageCount != 0 || transcriptWebVerifiedSnapshotEligible(after) {
				t.Fatalf("failed upgrade did not settle once without relabeling old rows: before=%+v after=%+v", before, after)
			}
			for range 3 {
				response = p3JSONRequest(t, fixture.server, http.MethodGet,
					"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
				assertTranscriptWebServingUnavailable(t, response, "message")
				if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
					t.Fatal(err)
				}
			}
			stable := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
			if stable.StateProjectionRevision != after.StateProjectionRevision {
				t.Fatalf("unchanged upgrade failure retried: before=%+v after=%+v", after, stable)
			}
			var sourceEventsAfter int
			if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=?`, fixture.stream.UID).Scan(&sourceEventsAfter); err != nil || sourceEventsAfter != sourceEventsBefore {
				t.Fatalf("repair changed canonical history count: before=%d after=%d err=%v", sourceEventsBefore, sourceEventsAfter, err)
			}
			frame, found, err := fixture.store.GetCompatibilityFrame(transcriptWebServingFrameID)
			if err != nil || !found || frame.Status != "failed" {
				t.Fatalf("history repair changed terminal task: found=%t frame=%+v err=%v", found, frame, err)
			}
		})
	}
}
