package transcript

import (
	"context"
	"testing"
	"time"
)

func TestClaimNextRunnerRecoversExpiredCheckpointlessAttempt(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-checkpointless-expired", "owner-a")
	if _, err := db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
	unrecoverable, err := repo.ListAllExpiredUnrecoverableRunnerCandidates(context.Background(), 10)
	if err != nil || len(unrecoverable) != 0 {
		t.Fatalf("checkpointless attempt was treated as unrecoverable: %#v err=%v", unrecoverable, err)
	}
	expired, err := repo.ListAllExpiredRunnerCandidates(context.Background(), 10)
	if err != nil || len(expired) != 0 {
		t.Fatalf("checkpointless attempt was treated as checkpoint resume: %#v err=%v", expired, err)
	}
	next, err := repo.ClaimNextRunner(context.Background(), ClaimNextRunnerInput{
		RunnerID: "runner-checkpointless-recovery", TTL: time.Minute,
	})
	if err != nil || !next.Claimed || next.Claim.ResumeSource != ResumeSourceFresh ||
		next.Claim.Attempt != claim.Attempt+1 || next.Claim.ClaimedInputRevision != claim.ClaimedInputRevision {
		t.Fatalf("checkpointless reclaim=%#v err=%v", next, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Attempt != next.Claim.Attempt || state.Status != "running" ||
		state.LastCheckpointSequence != 0 {
		t.Fatalf("reclaimed state=%#v found=%t err=%v", state, found, err)
	}
}

func TestClaimNextRunnerDoesNotStealLiveCheckpointlessAttempt(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-checkpointless-live", "owner-a")
	next, err := repo.ClaimNextRunner(context.Background(), ClaimNextRunnerInput{
		RunnerID: "runner-checkpointless-thief", TTL: time.Minute,
	})
	if err != nil || next.Claimed {
		t.Fatalf("live checkpointless claim was stolen: next=%#v err=%v", next, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Attempt != claim.Attempt || state.RunnerID != claim.RunnerID {
		t.Fatalf("live state=%#v found=%t err=%v", state, found, err)
	}
}
