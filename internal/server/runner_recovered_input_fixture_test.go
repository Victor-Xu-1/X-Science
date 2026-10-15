package server

import (
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Recovery-policy fixtures must checkpoint the input they actually own. An
// ordinary intake message accepted after a live claim is a new execution unit,
// not permission for its predecessor checkpoint to consume that message.
func claimAnsweredRecoveryFixtureInput(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	if _, _, created, err := f.repo.PauseRunnerForInput(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "fixture-intake-wait", Phase: transcriptstore.RunnerPhaseWaitingUser,
		Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`),
	}); err != nil || !created {
		t.Fatalf("pause intake predecessor: created=%t err=%v", created, err)
	}
	seedAnsweredTaskIntake(t, f.server, f.stream.OwnerID, f.stream.SessionID)
	claimed, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "fixture-intake-current",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision != f.claim.ClaimedInputRevision+1 {
		t.Fatalf("claim actual intake input: %+v %v", claimed, err)
	}
	f.claim = claimed.Claim
}
