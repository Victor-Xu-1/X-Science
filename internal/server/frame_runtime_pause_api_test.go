package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestExpiredInterruptionPauseDoesNotDependOnClockAvailability(t *testing.T) {
	for _, resumable := range []bool{false, true} {
		for _, unknownClock := range []bool{false, true} {
			name := "nonresumable"
			if resumable {
				name = "resumable"
			}
			if unknownClock {
				name += " unknown clock"
			} else {
				name += " valid clock"
			}
			t.Run(name, func(t *testing.T) {
				store, repository, db := newTranscriptWebFixture(t)
				const frameID = "frame-expired-pause-authority"
				seedTranscriptWebFrame(t, store, "local", "project-expired-pause-authority", frameID)
				stream, err := repository.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
					UID: "frame:" + frameID, OwnerID: "local", ExternalID: frameID, SessionID: frameID,
					Kind: transcriptstore.StreamKindFrameRef, ProjectID: "project-expired-pause-authority",
					RootFrameID: frameID, FrameID: frameID, Epoch: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := repository.AppendUserEvent(t.Context(), transcriptstore.AppendUserEventInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "pause-authority-input",
					PayloadJSON: []byte(`{"role":"user","text":"controlled pause authority"}`),
				}); err != nil {
					t.Fatal(err)
				}
				claim, err := repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "pause-authority-runner",
					TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
				})
				if err != nil || !claim.Claimed {
					t.Fatalf("claim=%+v err=%v", claim, err)
				}
				if _, err := repository.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
					Claim: claim.Claim, ClientMessageID: "pause-authority-interruption",
					ReasonCode: sessionRunnerSelectedSkillContractUnavailableReasonCode,
					AutoResume: false, Resumable: resumable,
				}); err != nil {
					t.Fatal(err)
				}
				if unknownClock {
					if _, err := db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`, claim.Claim.ClaimedAt.Add(-time.Second), stream.UID, claim.Claim.Attempt); err != nil {
						t.Fatal(err)
					}
				}
				app := New(Options{Workspace: store, Transcript: repository, FileRoot: t.TempDir()})
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := app.Close(ctx); err != nil {
						t.Errorf("close controlled server: %v", err)
					}
				})
				projected := compatJSONRequest(t, app.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
				if projected["runtime_elapsed_available"] != !unknownClock || projected["runtime_task_elapsed_available"] != !unknownClock {
					t.Fatalf("clock availability changed: %#v", projected)
				}
				if unknownClock && (projected["runtime_elapsed_ms"] != nil || projected["runtime_task_elapsed_ms"] != nil) {
					t.Fatalf("unavailable clock became a fake duration: %#v", projected)
				}
				conversation := compatJSONRequest(t, app.Handler(), http.MethodGet, "/api/conversations/"+frameID, "local", nil, http.StatusOK)
				runtime, _ := conversation["runtime"].(map[string]any)
				wantState, wantTaskStatus, wantFrameStatus := "idle", "error", "failed"
				if resumable {
					wantState, wantTaskStatus, wantFrameStatus = "paused", "pending", "paused"
				}
				if projected["status"] != wantFrameStatus || runtime["state"] != wantState || runtime["can_send_message"] != true || runtime["is_processing"] != false || runtime["has_task"] != resumable || runtime["task_status"] != wantTaskStatus || conversation["status"] != wantTaskStatus {
					t.Fatalf("resume authority changed with clock availability: frame=%#v conversation=%#v", projected, conversation)
				}
			})
		}
	}
}
