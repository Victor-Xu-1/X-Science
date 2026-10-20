package transcript

import (
	"fmt"
	"testing"
	"time"
)

func TestClaimedRunnerMemoryModeControlsCannotGrant(t *testing.T) {
	for _, ordinary := range []string{"on", "off"} {
		t.Run(ordinary, func(t *testing.T) {
			repo, _, stream := memoryAdmissionFixture(t, `{}`)
			input := memoryAdmissionInput(stream, "ordinary")
			input.RuntimeConfig = map[string]any{"memoryMode": ordinary}
			if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			first, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "first", TTL: time.Minute, ResumeSource: ResumeSourceFresh})
			if err != nil || !first.Claimed {
				t.Fatalf("claim=%+v err=%v", first, err)
			}
			checkpoint, _, _, err := repo.PauseRunnerForInput(t.Context(), AppendRunnerCheckpointInput{Claim: first.Claim, ClientMessageID: "question", Phase: RunnerPhaseWaitingUser, Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`)})
			if err != nil {
				t.Fatal(err)
			}
			control := "on"
			if ordinary == "on" {
				control = "off"
			}
			if err := repo.RunImmediate(t.Context(), func(tx *ImmediateTransaction) error {
				_, _, err := tx.AppendFrameInputResponse(t.Context(), AppendFrameInputResponseInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID, ClientMessageID: "answer", PayloadJSON: []byte(fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"question","content":"yes"}],"runtimeConfig":{"memoryMode":%q}}`, control))})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			resumed, err := repo.ClaimRunner(t.Context(), ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "resumed", TTL: time.Minute, ResumeSource: ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
			if err != nil || !resumed.Claimed {
				t.Fatalf("resume=%+v err=%v", resumed, err)
			}
			mode, found, err := repo.ClaimedRunnerMemoryMode(t.Context(), resumed.Claim)
			if err != nil || !found || mode != ordinary {
				t.Fatalf("control granted/replaced claim permission: %s %t %v", mode, found, err)
			}
			latest, _, err := repo.LatestFrameRuntimeConfig(t.Context(), stream.UID, stream.OwnerID)
			if err != nil || latest["memoryMode"] != control {
				t.Fatalf("general control projection changed: %v %v", latest, err)
			}
		})
	}
}
