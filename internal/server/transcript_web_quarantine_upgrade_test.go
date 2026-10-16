package server

import (
	"encoding/json"
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebProjectorUpgradeRepairsTerminalQuarantine(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		for _, path := range []string{"public_read", "background"} {
			t.Run(status+"/"+path, func(t *testing.T) {
				fixture := newTranscriptWebReadModelServingFixture(t, 3)
				var checkpointJSON []byte
				if err := fixture.db.QueryRow(`SELECT projector_state_json FROM transcript_web_projection_state
					WHERE stream_uid=? AND branch_id=?`, fixture.stream.UID, fixture.fence.BranchID).Scan(&checkpointJSON); err != nil {
					t.Fatal(err)
				}
				var checkpoint transcriptWebProjectorCheckpointV1
				if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
					t.Fatal(err)
				}
				checkpoint.Version = 13
				oldCheckpoint, err := json.Marshal(checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.db.Exec(`UPDATE transcript_web_projection_state SET
					projector_version=13,status='quarantined',last_error_code='projection_build_failed',
					projector_state_json=?,projector_state_sha256=? WHERE stream_uid=? AND branch_id=?`,
					string(oldCheckpoint), transcriptstore.TranscriptWebSHA256(oldCheckpoint), fixture.stream.UID, fixture.fence.BranchID); err != nil {
					t.Fatal(err)
				}
				if changed, err := fixture.store.TransitionCompatibilityFrameStatus(transcriptWebServingFrameID, "processing", status); err != nil || !changed {
					t.Fatalf("terminal fixture changed=%t err=%v", changed, err)
				}
				before, err := fixture.readModel.GetTranscriptWebProjectionUpgradeFence(t.Context(), fixture.stream.OwnerID, fixture.stream.UID, fixture.fence.BranchID)
				if err != nil || before.StateProjectorVersion != 13 || before.StateStatus != "quarantined" || before.VisibleMessageCount != 3 {
					t.Fatalf("old verified quarantine fixture=%+v err=%v", before, err)
				}
				if path == "background" {
					if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
						t.Fatal(err)
					}
				}
				response := p3JSONRequest(t, fixture.server, http.MethodGet,
					"/api/conversations/"+transcriptWebServingFrameID+"/messages", nil, "")
				if response.Code != http.StatusOK {
					t.Fatalf("projector upgrade hid retained %s history through %s: status=%d body=%s", status, path, response.Code, response.Body.String())
				}
				requireTranscriptWebServingPage(t, response, 3, 0, 2)
				after := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
				if !transcriptWebFenceReady(after) || after.StateProjectorVersion != transcriptstore.TranscriptWebProjectorVersion ||
					after.StateProjectionRevision <= before.StateProjectionRevision || after.SourceRevision != before.SourceRevision ||
					after.StateSourceChainSHA256 != before.StateSourceChainSHA256 {
					t.Fatalf("old terminal quarantine was not source-preservingly rebuilt: before=%+v after=%+v", before, after)
				}
				frame, found, err := fixture.store.GetCompatibilityFrame(transcriptWebServingFrameID)
				if err != nil || !found || frame.Status != status {
					t.Fatalf("history repair resumed or rewrote the task: found=%t status=%s err=%v", found, frame.Status, err)
				}
				if err := fixture.server.runTranscriptWebReadModelBatch(t.Context(), 1); err != nil {
					t.Fatal(err)
				}
				if stable := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID); stable.StateProjectionRevision != after.StateProjectionRevision {
					t.Fatalf("settled projector upgrade repeated: before=%+v after=%+v", after, stable)
				}
			})
		}
	}
}
