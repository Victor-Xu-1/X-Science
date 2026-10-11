package transcript

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// TerminalizeUnrecoverableRunnerCandidate fails one expired, non-resumable
// Frame attempt and its Frame only when that exact candidate is still the
// latest running lease, still owns the latest checkpoint, and still matches
// the current input generation. A newer claim, continuation, resumable
// checkpoint, or already-settled attempt is a no-op so an older recovery
// decision cannot fail live work.
func (r *Repository) TerminalizeUnrecoverableRunnerCandidate(
	ctx context.Context,
	candidate AutoResumeCandidate,
) (bool, error) {
	if r == nil || r.db == nil {
		return false, ErrSchemaUnavailable
	}
	candidate.StreamUID = strings.TrimSpace(candidate.StreamUID)
	candidate.OwnerID = strings.TrimSpace(candidate.OwnerID)
	candidate.FrameID = strings.TrimSpace(candidate.FrameID)
	candidate.ReasonCode = strings.TrimSpace(candidate.ReasonCode)
	if candidate.StreamUID == "" || candidate.OwnerID == "" || candidate.FrameID == "" ||
		candidate.Attempt <= 0 || candidate.CheckpointSequence <= 0 {
		return false, errors.New("unrecoverable runner candidate identity is incomplete")
	}
	if candidate.ReasonCode == "" {
		candidate.ReasonCode = "unrecoverable_runner_recovery"
	}
	applied := false
	err := r.RunImmediate(ctx, func(tx *ImmediateTransaction) error {
		ok, err := terminalizeUnrecoverableRunnerCandidateTx(ctx, tx, candidate, r.now().UTC())
		applied = ok
		return err
	})
	return applied, schemaError(err)
}

func terminalizeUnrecoverableRunnerCandidateTx(
	ctx context.Context,
	tx *ImmediateTransaction,
	candidate AutoResumeCandidate,
	now time.Time,
) (bool, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return false, errors.New("active transcript transaction is required")
	}
	active, err := latestFrameStreamConn(ctx, tx.conn, candidate.OwnerID, candidate.FrameID)
	if err != nil {
		if errors.Is(err, ErrSchemaUnavailable) {
			return false, nil
		}
		return false, err
	}
	if active.UID != candidate.StreamUID || active.Kind != StreamKindFrameRef {
		return false, nil
	}
	stream, err := getStreamConn(ctx, tx.conn, candidate.StreamUID, candidate.OwnerID)
	if err != nil {
		return false, err
	}
	if stream.FrameID != candidate.FrameID || stream.InputRevision != active.InputRevision {
		return false, nil
	}
	var attempt, claimedRevision, lastCheckpoint int64
	var status string
	var expiresAt time.Time
	err = tx.conn.QueryRowContext(ctx, `
		SELECT attempt,claimed_input_revision,status,last_checkpoint_sequence,expires_at
		FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID,
	).Scan(&attempt, &claimedRevision, &status, &lastCheckpoint, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attempt != candidate.Attempt || status != "running" || lastCheckpoint != candidate.CheckpointSequence ||
		claimedRevision != stream.InputRevision || expiresAt.After(now) {
		return false, nil
	}
	var checkpointSequence, checkpointAttempt int64
	var resumable bool
	var phase string
	err = tx.conn.QueryRowContext(ctx, `
		SELECT checkpoint.checkpoint_sequence,checkpoint.runner_attempt,checkpoint.resumable,checkpoint.phase
		FROM transcript_runner_checkpoints checkpoint
		WHERE checkpoint.stream_uid=? AND checkpoint.runner_attempt=?
		ORDER BY checkpoint.checkpoint_sequence DESC LIMIT 1`, stream.UID, attempt,
	).Scan(&checkpointSequence, &checkpointAttempt, &resumable, &phase)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if checkpointSequence != candidate.CheckpointSequence || checkpointAttempt != attempt || resumable ||
		phase == string(RunnerPhaseWaitingUser) || phase == string(RunnerPhaseWaitingApproval) ||
		phase == string(RunnerPhaseWaitingExternal) {
		return false, nil
	}
	var frameStatus string
	if err := tx.conn.QueryRowContext(ctx, `SELECT status FROM frames WHERE id=?`, stream.FrameID).Scan(&frameStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	normalizedFrame := normalizeTerminalStatus(frameStatus)
	if terminalStatus(normalizedFrame) && normalizedFrame != "failed" {
		return false, nil
	}
	_, _, created, err := tx.finishLatestFrameRunner(
		ctx, candidate.OwnerID, candidate.FrameID, "failed", candidate.ReasonCode, nil,
		normalizedFrame != "failed",
	)
	if err != nil {
		if errors.Is(err, ErrEventConflict) || errors.Is(err, ErrClaimStale) {
			return false, nil
		}
		return false, err
	}
	return created, nil
}
