package transcript

import (
	"context"
	"testing"
	"time"
)

func seedExpiredUnrecoverableCandidate(t *testing.T, streamUID string) (*Repository, AutoResumeCandidate, RunnerClaim) {
	t.Helper()
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, streamUID, "owner-a")
	interrupted, err := repo.InterruptRunner(context.Background(), InterruptRunnerInput{
		Claim: claim, ClientMessageID: streamUID + "-unrecoverable",
		ReasonCode: "automatic_recovery_exhausted", ResumeDetail: "cannot resume safely",
		AutoResume: false,
	})
	if err != nil || !interrupted.Created || interrupted.Checkpoint.Resumable {
		t.Fatalf("interruption=%#v err=%v", interrupted, err)
	}
	if _, err := db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
	candidates, err := repo.ListAllExpiredUnrecoverableRunnerCandidates(context.Background(), 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("unrecoverable candidates=%#v err=%v", candidates, err)
	}
	return repo, candidates[0], claim
}

func TestTerminalizeUnrecoverableRunnerCandidateSettlesExactExpiredAttempt(t *testing.T) {
	repo, candidate, claim := seedExpiredUnrecoverableCandidate(t, "stream-unrecoverable-settle")
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || !applied {
		t.Fatalf("applied=%t err=%v", applied, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Attempt != claim.Attempt || state.Status != "failed" {
		t.Fatalf("settled state=%#v found=%t err=%v", state, found, err)
	}
	var frameStatus string
	if err := repo.db.QueryRow(`SELECT status FROM frames WHERE id=?`, candidate.FrameID).Scan(&frameStatus); err != nil || frameStatus != "failed" {
		t.Fatalf("frame status=%q err=%v", frameStatus, err)
	}
	again, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || again {
		t.Fatalf("idempotent applied=%t err=%v", again, err)
	}
}

func TestTerminalizeUnrecoverableRunnerCandidatePreservesAlreadyFailedFrameMetadata(t *testing.T) {
	repo, candidate, claim := seedExpiredUnrecoverableCandidate(t, "stream-unrecoverable-already-failed")
	originalAt := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if _, err := repo.db.Exec(`UPDATE frames SET status='failed',updated_at=? WHERE id=?`, originalAt, candidate.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,status_description,completed_at,context_data)
		VALUES(?,?,?,?) ON CONFLICT(frame_id) DO UPDATE SET status_description=excluded.status_description,
		completed_at=excluded.completed_at,context_data=excluded.context_data`, candidate.FrameID,
		"original execution failure", originalAt, `{"budget":42}`); err != nil {
		t.Fatal(err)
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(t.Context(), candidate)
	if err != nil || !applied {
		t.Fatalf("orphan settlement applied=%t error=%v", applied, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(t.Context(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Status != "failed" || state.Attempt != claim.Attempt {
		t.Fatalf("orphan still unsettled: state=%#v found=%t error=%v", state, found, err)
	}
	assertTerminalPresentation(t, repo.db, candidate.FrameID, "failed", "original execution failure")
	assertTerminalCompletedAt(t, repo.db, candidate.FrameID, &originalAt)
	var updatedAt time.Time
	var contextData string
	if err := repo.db.QueryRow(`SELECT frame.updated_at,metadata.context_data FROM frames frame
		JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id WHERE frame.id=?`, candidate.FrameID).
		Scan(&updatedAt, &contextData); err != nil || !updatedAt.Equal(originalAt) || contextData != `{"budget":42}` {
		t.Fatalf("prior frame state changed: updated=%v context=%q error=%v", updatedAt, contextData, err)
	}
	again, err := repo.TerminalizeUnrecoverableRunnerCandidate(t.Context(), candidate)
	if err != nil || again {
		t.Fatalf("repeat settlement applied=%t error=%v", again, err)
	}
}

func TestTerminalizeUnrecoverableRunnerCandidateIgnoresNewerClaim(t *testing.T) {
	repo, candidate, claim := seedExpiredUnrecoverableCandidate(t, "stream-unrecoverable-newer-claim")
	next, err := repo.ClaimRunner(context.Background(), ClaimRunnerInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "runner-newer",
		TTL: time.Minute, ResumeSource: ResumeSourceFresh,
	})
	if err != nil || !next.Claimed || next.Claim.Attempt <= claim.Attempt {
		t.Fatalf("newer claim=%#v err=%v", next, err)
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || applied {
		t.Fatalf("stale terminalize applied=%t err=%v", applied, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Attempt != next.Claim.Attempt || state.Status != "running" {
		t.Fatalf("live state=%#v found=%t err=%v", state, found, err)
	}
	var frameStatus string
	if err := repo.db.QueryRow(`SELECT status FROM frames WHERE id=?`, candidate.FrameID).Scan(&frameStatus); err != nil {
		t.Fatal(err)
	}
	if frameStatus == "failed" || frameStatus == "completed" || frameStatus == "cancelled" || frameStatus == "canceled" {
		t.Fatalf("newer claim frame was terminalized: status=%q", frameStatus)
	}
}

func TestTerminalizeUnrecoverableRunnerCandidateIgnoresNewerInput(t *testing.T) {
	repo, candidate, claim := seedExpiredUnrecoverableCandidate(t, "stream-unrecoverable-newer-input")
	if _, created, err := repo.AppendUserEvent(context.Background(), AppendUserEventInput{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, ClientMessageID: "user-after-unrecoverable",
		PayloadJSON: []byte(`{"text":"continue with a new instruction"}`),
	}); err != nil || !created {
		t.Fatalf("newer input created=%t err=%v", created, err)
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || applied {
		t.Fatalf("stale terminalize applied=%t err=%v", applied, err)
	}
	next, err := repo.ClaimNextRunner(context.Background(), ClaimNextRunnerInput{
		RunnerID: "runner-after-new-input", TTL: time.Minute,
	})
	if err != nil || !next.Claimed || next.Claim.ResumeSource != ResumeSourceFresh ||
		next.Claim.Attempt <= claim.Attempt {
		t.Fatalf("new input reclaim=%#v err=%v", next, err)
	}
	var frameStatus string
	if err := repo.db.QueryRow(`SELECT status FROM frames WHERE id=?`, candidate.FrameID).Scan(&frameStatus); err != nil {
		t.Fatal(err)
	}
	if frameStatus == "failed" {
		t.Fatalf("newer input frame was failed by stale candidate")
	}
}

func TestTerminalizeUnrecoverableRunnerCandidateIgnoresNewerResumableCheckpoint(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-unrecoverable-later-resumable", "owner-a")
	staleCheckpoint, _, created, err := repo.AppendRunnerCheckpoint(context.Background(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "later-resumable-unrecoverable", Phase: RunnerPhaseExecuting,
		Resumable: false, PayloadJSON: []byte(`{"reason_code":"automatic_recovery_exhausted"}`),
	})
	if err != nil || !created {
		t.Fatalf("unrecoverable checkpoint=%#v created=%t err=%v", staleCheckpoint, created, err)
	}
	stale := AutoResumeCandidate{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, FrameID: "frame-a",
		Attempt: claim.Attempt, CheckpointSequence: staleCheckpoint.Sequence,
		ReasonCode: "automatic_recovery_exhausted",
	}
	repaired, _, created, err := repo.AppendRunnerCheckpoint(context.Background(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "later-resumable-repair", Phase: RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: []byte(`{"auto_resume":true,"step":"repaired"}`),
	})
	if err != nil || !created || repaired.Sequence <= staleCheckpoint.Sequence {
		t.Fatalf("resumable checkpoint=%#v created=%t err=%v", repaired, created, err)
	}
	if _, err := db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), stale)
	if err != nil || applied {
		t.Fatalf("stale terminalize applied=%t err=%v", applied, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Status != "running" || state.LastCheckpointSequence != repaired.Sequence {
		t.Fatalf("repaired state=%#v found=%t err=%v", state, found, err)
	}
}

func TestTerminalizeUnrecoverableRunnerCandidateIgnoresSettledAndLiveAttempts(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	claim := seedArtifactProjectionClaim(t, repo, db, "stream-unrecoverable-settled", "owner-a")
	checkpoint, _, created, err := repo.AppendRunnerCheckpoint(context.Background(), AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "settled-unrecoverable", Phase: RunnerPhaseExecuting,
		Resumable: false, PayloadJSON: []byte(`{"reason_code":"automatic_recovery_exhausted"}`),
	})
	if err != nil || !created {
		t.Fatalf("unrecoverable checkpoint=%#v created=%t err=%v", checkpoint, created, err)
	}
	candidate := AutoResumeCandidate{
		StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, FrameID: "frame-a",
		Attempt: claim.Attempt, CheckpointSequence: checkpoint.Sequence,
		ReasonCode: "automatic_recovery_exhausted",
	}
	applied, err := repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || applied {
		t.Fatalf("live lease terminalize applied=%t err=%v", applied, err)
	}
	if _, _, _, err := repo.FinishRunner(context.Background(), FinishRunnerInput{
		Claim: claim, ClientMessageID: "finish-settled-unrecoverable", Status: "completed",
		PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE transcript_runner_attempts SET expires_at=? WHERE stream_uid=? AND attempt=?`,
		time.Now().UTC().Add(-time.Minute), claim.StreamUID, claim.Attempt); err != nil {
		t.Fatal(err)
	}
	applied, err = repo.TerminalizeUnrecoverableRunnerCandidate(context.Background(), candidate)
	if err != nil || applied {
		t.Fatalf("completed attempt terminalize applied=%t err=%v", applied, err)
	}
	state, found, err := repo.GetLatestRunnerRuntimeState(context.Background(), claim.StreamUID, claim.OwnerID)
	if err != nil || !found || state.Status != "completed" {
		t.Fatalf("completed state=%#v found=%t err=%v", state, found, err)
	}
	var frameStatus string
	if err := db.QueryRow(`SELECT status FROM frames WHERE id='frame-a'`).Scan(&frameStatus); err != nil || frameStatus != "completed" {
		t.Fatalf("completed frame status=%q err=%v", frameStatus, err)
	}
}
