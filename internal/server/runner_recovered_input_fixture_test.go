package server

import (
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Recovery-policy fixtures answer a genuine waiting checkpoint before testing
// their later provider recovery. The answer advances the owned input revision
// while preserving this same logical attempt and its exact continuation.
func claimAnsweredRecoveryFixtureInput(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	checkpoint, _, created, err := f.repo.PauseRunnerForInput(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: f.claim, ClientMessageID: "fixture-intake-wait", Phase: transcriptstore.RunnerPhaseWaitingUser,
		Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_user_response"}`),
	})
	if err != nil || !created {
		t.Fatalf("pause intake predecessor: created=%t err=%v", created, err)
	}
	seedAnsweredTaskIntake(t, f.server, f.stream.OwnerID, f.stream.SessionID)
	claimed, err := f.repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "fixture-intake-current",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed || claimed.Claim.ClaimedInputRevision != f.claim.ClaimedInputRevision+1 ||
		claimed.Claim.Attempt != f.claim.Attempt || claimed.Claim.ResumeSource != transcriptstore.ResumeSourceCheckpoint ||
		claimed.Claim.ResumeCheckpoint != checkpoint.Sequence || claimed.Claim.ResumeCheckpointAttempt != f.claim.Attempt {
		t.Fatalf("claim actual intake input: %+v %v", claimed, err)
	}
	f.claim = claimed.Claim
}
