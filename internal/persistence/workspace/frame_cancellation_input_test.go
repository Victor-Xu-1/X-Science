package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestFrameCancellationExpectedInputRejectsNewerFollowup(t *testing.T) {
	for _, mode := range []string{"queued", "delivering", "admitted"} {
		t.Run(mode, func(t *testing.T) {
			store, repo, stream, claim := newTranscriptCancellationFixture(t)
			if mode == "admitted" {
				if _, _, _, err := repo.AppendFrameUserEvent(context.Background(), transcriptstore.AppendFrameUserEventInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "new-followup", FrameEventID: "new-followup-event", MessageUUID: "new-followup-message", Text: "Newer user input",
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, _, _, err := store.QueueCompatibilityMessage("frame-a", "new-followup", map[string]any{"request": "Newer user input"}, map[string]any{"text": "Newer user input"}); err != nil {
					t.Fatal(err)
				}
				if mode == "delivering" {
					if _, err := store.db.Exec(`UPDATE queued_user_messages SET state='delivering' WHERE intent_id='new-followup'`); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := store.CancelCompatibilityFrameTreeWithTranscript(context.Background(), "frame-a", "late close", "user-a"); !errors.Is(err, ErrFrameCancellationInputStale) {
				t.Fatalf("late cancel=%v", err)
			}
			if mode == "admitted" {
				if _, err := store.CancelCompatibilityFrameTreeWithTranscript(context.Background(), "frame-a", "wrong running input", "new-followup"); !errors.Is(err, ErrFrameCancellationInputStale) {
					t.Fatalf("cancel older active prompt=%v", err)
				}
			}
			if _, err := repo.ValidateLiveRunnerClaim(context.Background(), claim); err != nil {
				t.Fatalf("rejected cancel touched runner: %v", err)
			}
			frame, found, err := store.GetFrame("frame-a")
			if err != nil || !found || frame.Status != "processing" {
				t.Fatalf("frame=%#v %v", frame, err)
			}
			var cancellations int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_cancelled'`).Scan(&cancellations); err != nil || cancellations != 0 {
				t.Fatalf("cancellation facts=%d %v", cancellations, err)
			}
		})
	}
}

func TestFrameCancellationExpectedInputAcceptsCurrentOrUnclaimedInput(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-claim", true: "running"}[running], func(t *testing.T) {
			store, repo, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
			if running {
				claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "scope-runner", ResumeSource: transcriptstore.ResumeSourceFresh, TTL: time.Minute,
				})
				if err != nil || !claimed.Claimed {
					t.Fatalf("claim=%#v %v", claimed, err)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				result, err := store.CancelCompatibilityFrameTreeWithTranscript(context.Background(), "frame-a", "owned close", "user-a")
				if err != nil || result.RootFrameID != "frame-a" {
					t.Fatalf("cancel=%#v %v", result, err)
				}
			}
			state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), stream.UID, stream.OwnerID)
			if err != nil || !found || state.Status != "cancelled" {
				t.Fatalf("state=%#v %v", state, err)
			}
			var inputs, receipts int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type='user_message'`, stream.UID).Scan(&inputs); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_runner_receipts WHERE stream_uid=?`, stream.UID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if inputs != 1 || receipts != 1 {
				t.Fatalf("cancel lost data or duplicated terminal: inputs=%d receipts=%d", inputs, receipts)
			}
		})
	}
}
