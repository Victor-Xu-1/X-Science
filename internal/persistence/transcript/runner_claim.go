package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (r *Repository) ClaimNextFrameRunner(ctx context.Context, input ClaimNextFrameRunnerInput) (ClaimNextFrameRunnerResult, error) {
	result, err := r.claimNextRunner(ctx, ClaimNextRunnerInput(input), false)
	return ClaimNextFrameRunnerResult(result), err
}

// ClaimNextRunner selects pending production work from every runner-backed
// stream kind currently supported by the server execution path.
func (r *Repository) ClaimNextRunner(ctx context.Context, input ClaimNextRunnerInput) (ClaimNextRunnerResult, error) {
	return r.claimNextRunner(ctx, input, true)
}

func (r *Repository) claimNextRunner(
	ctx context.Context, input ClaimNextRunnerInput, includeStandalone bool,
) (ClaimNextRunnerResult, error) {
	input.RunnerID = strings.TrimSpace(input.RunnerID)
	if input.RunnerID == "" || input.TTL <= 0 {
		return ClaimNextRunnerResult{}, errors.New("runner id and positive ttl are required")
	}
	// This lock protects only the in-memory fairness hint. It is independent of
	// inputWakeMu: committing a claim may signal the existing input wake.
	r.candidateMu.Lock()
	defer r.candidateMu.Unlock()
	index := 0
	if includeStandalone {
		index = 1
	}
	cursor := r.candidateCursor[index]
	var result ClaimNextRunnerResult
	var selectionErr error
	err := r.withImmediate(ctx, func(conn *sql.Conn) error {
		if _, err := reconcileTerminalFrameRunnerAttemptsConn(ctx, &ImmediateTransaction{repository: r, conn: conn}, 16); err != nil {
			return err
		}
		now := r.now().UTC()
		for scanned := 0; scanned < 32; scanned++ {
			stream, next, found, err := nextRunnerCandidateConn(ctx, conn, includeStandalone, now, cursor)
			if err != nil {
				return err
			}
			if !found {
				cursor = runnerCandidateCursor{}
				return nil
			}
			cursor = next
			source, checkpoint, claimable, err := r.automaticFrameResumeRequestConn(ctx, conn, stream, now)
			if err != nil {
				// Revalidation has not mutated the candidate. Commit only the existing
				// reconciliation and fairness hint, while reporting the source error.
				// Never commit a failed claim's partial writes or turn conflict into work.
				selectionErr = err
				return nil
			}
			if !claimable {
				continue
			}
			claimed, err := r.claimRunnerConn(ctx, conn, stream, ClaimRunnerInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: input.RunnerID, TTL: input.TTL, ResumeSource: source, ResumeCheckpoint: checkpoint}, now)
			if err != nil {
				return err
			}
			if !claimed.Claimed {
				continue
			}
			stream, err = getStreamConn(ctx, conn, stream.UID, stream.OwnerID)
			if err != nil {
				return err
			}
			result = ClaimNextRunnerResult{Stream: stream, Claim: claimed.Claim, Claimed: true}
			return nil
		}
		return nil
	})
	// Failed transactions never advance the hint. An empty bounded page may
	// advance it; the existing one-second worker poll continues the next page.
	if err == nil {
		r.candidateCursor[index] = cursor
	}
	return result, schemaError(errors.Join(err, selectionErr))
}

func (r *Repository) automaticFrameResumeRequestConn(
	ctx context.Context,
	conn *sql.Conn,
	stream Stream,
	now time.Time,
) (ResumeSource, int64, bool, error) {
	var attempt int64
	var status string
	var expiresAt time.Time
	var claimedInputRevision int64
	recoveryWork := false
	err := conn.QueryRowContext(ctx, `
		SELECT attempt,status,expires_at,claimed_input_revision FROM transcript_runner_attempts
		WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID,
	).Scan(&attempt, &status, &expiresAt, &claimedInputRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ResumeSourceFresh, 0, true, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	if claimedInputRevision < stream.InputRevision {
		evidence, err := r.freshMessageEvidenceConn(ctx, conn, stream)
		if err != nil {
			return "", 0, false, err
		}
		if evidence.Authorized {
			return ResumeSourceFresh, 0, true, nil
		}
		coverage := evidence.Coverage
		if coverage.Pending() && !coverage.Complete {
			return "", 0, false, nil
		}
		recoveryWork = coverage.RecoveryWork()
	}
	if claimedInputRevision > stream.InputRevision {
		return "", 0, false, ErrEventConflict
	}
	if status != "running" {
		if recoveryWork {
			// A completed round no longer carries a prior wait. Other saved
			// states (including historical reclaimed attempts) retain an
			// explicit nonautomatic checkpoint until an authorized new input
			// or continuation supersedes it.
			if status != "completed" {
				var payload string
				err := conn.QueryRowContext(ctx, `SELECT e.payload_json FROM transcript_runner_checkpoints c
					JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.event_id
					WHERE c.stream_uid=? AND c.runner_attempt=? ORDER BY c.checkpoint_sequence DESC LIMIT 1`, stream.UID, attempt).Scan(&payload)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return "", 0, false, err
				}
				if err == nil && !runnerCheckpointAllowsAutomaticResume(payload) {
					return "", 0, false, nil
				}
			}
			return ResumeSourceRetry, 0, true, nil
		}
		return "", 0, false, nil
	}
	if expiresAt.After(now) {
		return "", 0, false, nil
	}
	var checkpoint int64
	var resumable bool
	var rawPayload string
	err = conn.QueryRowContext(ctx, `
		SELECT checkpoint.checkpoint_sequence,checkpoint.resumable,events.payload_json
		FROM transcript_runner_checkpoints checkpoint
		JOIN transcript_events events
			ON events.stream_uid=checkpoint.stream_uid AND events.event_id=checkpoint.event_id
		WHERE checkpoint.stream_uid=? AND checkpoint.runner_attempt=?
		ORDER BY checkpoint.checkpoint_sequence DESC LIMIT 1`, stream.UID, attempt,
	).Scan(&checkpoint, &resumable, &rawPayload)
	if errors.Is(err, sql.ErrNoRows) {
		if recoveryWork {
			return ResumeSourceRetry, 0, true, nil
		}
		// Missing checkpoints are not proof of non-dispatch. Only the typed
		// preparation admission and unchanged phase permit a fresh attempt.
		if claimedInputRevision == stream.InputRevision {
			safe, err := runnerPreparationIsSafeConn(ctx, conn, stream.UID, attempt, claimedInputRevision)
			return ResumeSourceFresh, 0, safe, err
		}
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	if !runnerCheckpointAllowsAutomaticResume(rawPayload) {
		return "", 0, false, nil
	}
	if !resumable {
		if recoveryWork {
			return ResumeSourceRetry, 0, true, nil
		}
		return "", 0, false, nil
	}
	// The durable Frame resume dispatcher owns a stream only while its dispatch
	// is registered, claimed, or blocked (filtered by claimNextRunner above).
	// Without such an owner, the ordinary runner pool must resume every durable
	// checkpoint, including reason-coded self-repair boundaries; otherwise a
	// language, provider, or tool recovery can remain processing forever.
	return ResumeSourceCheckpoint, checkpoint, true, nil
}

// runnerCheckpointAllowsAutomaticResume separates execution recovery from a
// durable wait for user-owned external state. Legacy checkpoints predate the
// explicit flag and remain recoverable because resumable historically meant
// automatic; a present false or malformed flag fails closed to explicit wake.
func runnerCheckpointAllowsAutomaticResume(rawPayload string) bool {
	var payload map[string]any
	if json.Unmarshal([]byte(rawPayload), &payload) != nil {
		return false
	}
	value, present := payload["auto_resume"]
	if !present {
		return true
	}
	autoResume, valid := value.(bool)
	return valid && autoResume
}

// LatestRunnerInputEventType returns the most recent runner input event type
// (user_message or user_input_response) as a publication fact for the existing
// plan-response guard. It is not admission coverage or execution handoff
// authority; scheduling and resume consumers use GetInputCoverage instead.
func (r *Repository) LatestRunnerInputEventType(
	ctx context.Context,
	streamUID, ownerID string,
) (string, error) {
	if r == nil || r.db == nil {
		return "", ErrSchemaUnavailable
	}
	streamUID = strings.TrimSpace(streamUID)
	ownerID = strings.TrimSpace(ownerID)
	if streamUID == "" || ownerID == "" {
		return "", errors.New("stream uid and owner are required")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	var storedOwner string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM transcript_streams WHERE stream_uid=?`, streamUID).Scan(&storedOwner); err != nil {
		return "", schemaError(err)
	}
	if storedOwner != ownerID {
		return "", ErrOwnerMismatch
	}
	var eventType string
	if err := tx.QueryRowContext(ctx, `
		SELECT event_type FROM transcript_events
		WHERE stream_uid=? AND event_type IN ('user_message','user_input_response')
		ORDER BY publication_seq DESC LIMIT 1`, streamUID,
	).Scan(&eventType); err != nil {
		return "", schemaError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", schemaError(err)
	}
	return eventType, nil
}

func (r *Repository) claimRunnerConn(
	ctx context.Context,
	conn *sql.Conn,
	stream Stream,
	input ClaimRunnerInput,
	now time.Time,
) (ClaimRunnerResult, error) {
	var result ClaimRunnerResult
	blocked, err := frameTerminalBlocksRunnerClaimConn(ctx, conn, stream)
	if err != nil || blocked {
		return result, err
	}
	var activeRunner string
	var activeAttempt int64
	var activeExpires time.Time
	err = conn.QueryRowContext(ctx, `
		SELECT attempt,runner_id,expires_at FROM transcript_runner_attempts
		WHERE stream_uid=? AND status='running' ORDER BY attempt DESC LIMIT 1`, stream.UID,
	).Scan(&activeAttempt, &activeRunner, &activeExpires)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil && activeExpires.After(now) {
		result.OwnerRunnerID = activeRunner
		return result, nil
	}
	var latestAttempt, latestClaimedRevision, latestCheckpoint int64
	var latestStatus, latestPhase string
	latestErr := conn.QueryRowContext(ctx, `
		SELECT attempt,claimed_input_revision,status,phase,last_checkpoint_sequence
		FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID,
	).Scan(&latestAttempt, &latestClaimedRevision, &latestStatus, &latestPhase, &latestCheckpoint)
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		return result, latestErr
	}
	var checkpointAttempt int64
	reusesReclaimedAttempt := false
	if errors.Is(latestErr, sql.ErrNoRows) {
		if input.ResumeSource == ResumeSourceUserInput {
			authorized, err := claimedFrameResumeAuthorizesRunnerConn(ctx, conn, stream, input.RunnerID)
			if err != nil {
				return result, err
			}
			if !authorized {
				return result, nil
			}
		} else if input.ResumeSource != ResumeSourceFresh {
			return result, nil
		}
	} else {
		switch input.ResumeSource {
		case ResumeSourceFresh:
			if stream.InputRevision < latestClaimedRevision {
				return result, nil
			}
			if stream.InputRevision > latestClaimedRevision {
				evidence, err := r.freshMessageEvidenceConn(ctx, conn, stream)
				if err != nil {
					return result, err
				}
				if !evidence.Authorized {
					return result, nil
				}
			} else {
				// No newer input arrived. A fresh claim may supersede the previous
				// attempt only when that attempt is not terminal and has no
				// resumable provider checkpoint (for example a scientific-compute
				// holder re-adopted after a lease expiry). A resumable checkpoint
				// must be continued in place, not replaced by a fresh attempt.
				switch latestStatus {
				case "completed", "failed", "cancelled", "canceled":
					return result, nil
				}
				if latestCheckpoint == 0 {
					safe, err := runnerPreparationIsSafeConn(ctx, conn, stream.UID, latestAttempt, latestClaimedRevision)
					if err != nil || !safe {
						return result, err
					}
				}
				var resumable bool
				err := conn.QueryRowContext(ctx, `
					SELECT resumable FROM transcript_runner_checkpoints
					WHERE stream_uid=? AND runner_attempt=? AND resumable=1
					ORDER BY checkpoint_sequence DESC LIMIT 1`, stream.UID, latestAttempt,
				).Scan(&resumable)
				if err == nil {
					return result, nil
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return result, err
				}
			}
		case ResumeSourceCheckpoint:
			reusesLiveAttempt := activeAttempt != 0 && activeAttempt == latestAttempt &&
				latestStatus == "running" && !activeExpires.After(now)
			explicitResume, resumeErr := claimedFrameResumeAuthorizesRunnerConn(ctx, conn, stream, input.RunnerID)
			if resumeErr != nil {
				return result, resumeErr
			}
			if err := conn.QueryRowContext(ctx, `
				SELECT runner_attempt FROM transcript_runner_checkpoints
				WHERE stream_uid=? AND checkpoint_sequence=? AND resumable=1`,
				stream.UID, input.ResumeCheckpoint,
			).Scan(&checkpointAttempt); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					err = ErrCheckpointUnavailable
				}
				return result, err
			}
			// A lease-expired attempt that was later reconciled to "reclaimed"
			// is still the same logical execution unit. Provider continuation
			// checkpoints are bound to their parent attempt, so the resume must
			// revive that attempt instead of starting a new parent attempt.
			// Real terminal outcomes (failed/cancelled) still start a fresh
			// authorized attempt.
			reusesReclaimedAttempt = activeAttempt == 0 && explicitResume &&
				latestAttempt == checkpointAttempt && latestStatus == "reclaimed"
			startsAuthorizedAttempt := activeAttempt == 0 && explicitResume &&
				(latestStatus == "failed" || latestStatus == "cancelled")
			if (!reusesLiveAttempt && !reusesReclaimedAttempt && !startsAuthorizedAttempt) ||
				stream.InputRevision < latestClaimedRevision {
				return result, ErrCheckpointUnavailable
			}
			if stream.InputRevision > latestClaimedRevision {
				evidence, err := r.freshMessageEvidenceConn(ctx, conn, stream)
				if err != nil {
					return result, err
				}
				if evidence.Authorized || !evidence.Coverage.Complete && !explicitResume {
					return result, ErrCheckpointUnavailable
				}
			} else if stream.InputRevision == stream.ConsumedInputRevision &&
				(latestPhase == string(RunnerPhaseWaitingUser) || latestPhase == string(RunnerPhaseWaitingApproval)) {
				return result, nil
			}
		case ResumeSourceUserInput:
			authorized, err := claimedFrameResumeAuthorizesRunnerConn(ctx, conn, stream, input.RunnerID)
			if err != nil {
				return result, err
			}
			if !authorized {
				return result, nil
			}
		case ResumeSourceRetry:
			coverage, err := r.inputCoverageConn(ctx, conn, stream, latestClaimedRevision)
			if err != nil {
				return result, err
			}
			if !coverage.RecoveryWork() {
				return result, nil
			}
			if latestStatus != "completed" && latestCheckpoint > 0 {
				var resumable bool
				var payload string
				err := conn.QueryRowContext(ctx, `SELECT c.resumable,e.payload_json FROM transcript_runner_checkpoints c
						JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.event_id
						WHERE c.stream_uid=? AND c.runner_attempt=? AND c.checkpoint_sequence=?`,
					stream.UID, latestAttempt, latestCheckpoint).Scan(&resumable, &payload)
				if err != nil {
					return result, err
				}
				if !runnerCheckpointAllowsAutomaticResume(payload) || latestStatus == "running" && resumable {
					return result, ErrCheckpointUnavailable
				}
			}
		default:
			return result, nil
		}
	}
	claimedRevision, claimable, err := claimableInputRevisionConn(ctx, conn, stream, input.ResumeSource, now)
	if err != nil || !claimable {
		return result, err
	}
	if err := validateResumeCheckpointConn(
		ctx, conn, stream.UID, input.ResumeSource, input.ResumeCheckpoint, claimedRevision,
		stream.InputRevision > stream.ConsumedInputRevision, now,
	); err != nil {
		return result, err
	}
	token, digest, err := r.newClaimToken()
	if err != nil {
		return result, err
	}
	expires := now.Add(input.TTL)
	if input.ResumeSource == ResumeSourceCheckpoint && (activeAttempt > 0 || reusesReclaimedAttempt) {
		reuseAttempt := activeAttempt
		if reuseAttempt == 0 {
			reuseAttempt = checkpointAttempt
		}
		if checkpointAttempt != reuseAttempt {
			return result, ErrCheckpointUnavailable
		}
		timingFact, err := prepareRunnerTimingResumeFactConn(ctx, conn, stream, reuseAttempt, claimedRevision, digest, now)
		if err != nil {
			return result, err
		}
		if activeAttempt > 0 {
			updated, err := conn.ExecContext(ctx, `
				UPDATE transcript_runner_attempts
				SET runner_id=?,claim_token_sha256=?,claimed_input_revision=?,resume_source=?,resume_checkpoint_sequence=?,expires_at=?
				WHERE stream_uid=? AND attempt=? AND status='running' AND expires_at<=?`,
				input.RunnerID, digest, claimedRevision, string(input.ResumeSource), input.ResumeCheckpoint, expires,
				stream.UID, reuseAttempt, now,
			)
			if err != nil {
				return result, err
			}
			changed, err := updated.RowsAffected()
			if err != nil {
				return result, err
			}
			if changed != 1 {
				return result, ErrClaimStale
			}
		} else {
			updated, err := conn.ExecContext(ctx, `
				UPDATE transcript_runner_attempts
				SET runner_id=?,claim_token_sha256=?,claimed_input_revision=?,resume_source=?,resume_checkpoint_sequence=?,
					status='running',phase='claimed',phase_sequence=phase_sequence+1,
					finished_event_id=NULL,finished_at=NULL,expires_at=?
				WHERE stream_uid=? AND attempt=? AND status='reclaimed'`,
				input.RunnerID, digest, claimedRevision, string(input.ResumeSource), input.ResumeCheckpoint,
				expires, stream.UID, reuseAttempt,
			)
			if err != nil {
				return result, err
			}
			changed, err := updated.RowsAffected()
			if err != nil {
				return result, err
			}
			if changed != 1 {
				return result, ErrClaimStale
			}
			// The reclaimed receipt represented an expired lease, not a real
			// terminal outcome. Reviving the same attempt invalidates it so a
			// later FinishRunner can settle the revived execution unit.
			if _, err := conn.ExecContext(ctx, `
				DELETE FROM transcript_runner_receipts WHERE stream_uid=? AND attempt=?`,
				stream.UID, reuseAttempt); err != nil {
				return result, err
			}
		}
		if err := resumeWaitingFrameForRunnerConn(ctx, conn, stream, now); err != nil {
			return result, err
		}
		if err := appendRunnerTimingResumeFactConn(ctx, conn, stream, timingFact); err != nil {
			return result, err
		}
		result.Claimed = true
		result.OwnerRunnerID = input.RunnerID
		result.Claim = RunnerClaim{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: input.RunnerID,
			Attempt: reuseAttempt, ClaimToken: token, ClaimedInputRevision: claimedRevision,
			ResumeSource: input.ResumeSource, ResumeCheckpoint: input.ResumeCheckpoint,
			ResumeCheckpointAttempt: checkpointAttempt,
			ClaimedAt:               now, ExpiresAt: expires,
		}
		return result, nil
	}
	var attempt int64
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM transcript_runner_attempts WHERE stream_uid=?`, stream.UID).Scan(&attempt); err != nil {
		return result, err
	}
	if activeAttempt > 0 {
		stream, err = reclaimExpiredRunnerConn(ctx, conn, stream, activeAttempt, now)
		if err != nil {
			return result, err
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO transcript_runner_attempts(
			stream_uid,attempt,runner_id,claim_token_sha256,claimed_input_revision,
			resume_source,resume_checkpoint_sequence,status,phase,phase_sequence,
			last_checkpoint_sequence,claimed_at,expires_at
		) VALUES(?,?,?,?,?,?,?,'running','claimed',1,0,?,?)`,
		stream.UID, attempt, input.RunnerID, digest, claimedRevision,
		string(input.ResumeSource), input.ResumeCheckpoint, now, expires,
	); err != nil {
		return result, err
	}
	if err := resumeWaitingFrameForRunnerConn(ctx, conn, stream, now); err != nil {
		return result, err
	}
	result.Claimed = true
	result.OwnerRunnerID = input.RunnerID
	result.Claim = RunnerClaim{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: input.RunnerID,
		Attempt: attempt, ClaimToken: token, ClaimedInputRevision: claimedRevision,
		ResumeSource: input.ResumeSource, ResumeCheckpoint: input.ResumeCheckpoint,
		ResumeCheckpointAttempt: checkpointAttempt,
		ClaimedAt:               now, ExpiresAt: expires,
	}
	if err := appendRunnerPreparationAdmissionConn(ctx, conn, stream, result.Claim, now); err != nil {
		return ClaimRunnerResult{}, err
	}
	return result, nil
}

func claimedFrameResumeAuthorizesRunnerConn(
	ctx context.Context,
	conn *sql.Conn,
	stream Stream,
	runnerID string,
) (bool, error) {
	if stream.Kind != StreamKindFrameRef {
		return false, nil
	}
	const prefix = "frame-resume:"
	runnerID = strings.TrimSpace(runnerID)
	if !strings.HasPrefix(runnerID, prefix) {
		return false, nil
	}
	resumeEventID := strings.TrimSpace(strings.TrimPrefix(runnerID, prefix))
	if resumeEventID == "" {
		return false, nil
	}
	var count int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM frame_events
		WHERE id=? AND frame_id=? AND event_type='frame_resumed'
			AND json_extract(payload,'$.dispatch.status')='claimed'`,
		resumeEventID, stream.FrameID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

func activateFrameForNewInputConn(ctx context.Context, conn *sql.Conn, stream Stream, now time.Time) error {
	if stream.Kind != StreamKindFrameRef {
		return nil
	}
	result, err := conn.ExecContext(ctx, `
		UPDATE frames SET status='processing',updated_at=?
		WHERE id=? AND status IN ('completed','failed','cancelled','canceled','awaiting_user_response','awaiting_plan_approval')`, now, stream.FrameID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO frame_runtime_metadata (frame_id,status_description,completed_at,context_data)
		VALUES(?,'',NULL,'{}')
		ON CONFLICT(frame_id) DO UPDATE SET
			status_description='',completed_at=NULL`, stream.FrameID)
	return err
}

func resumeWaitingFrameForRunnerConn(ctx context.Context, conn *sql.Conn, stream Stream, now time.Time) error {
	if stream.Kind != StreamKindFrameRef {
		return nil
	}
	_, err := conn.ExecContext(ctx, `
		UPDATE frames SET status='processing',updated_at=?
		WHERE id=? AND status IN ('awaiting_user_response','awaiting_plan_approval')`, now, stream.FrameID)
	return err
}

func frameTerminalBlocksRunnerClaimConn(
	ctx context.Context,
	conn *sql.Conn,
	stream Stream,
) (bool, error) {
	_, terminal, err := frameTerminalStatusConn(ctx, conn, stream)
	return terminal, err
}

func frameTerminalStatusConn(
	ctx context.Context,
	conn *sql.Conn,
	stream Stream,
) (string, bool, error) {
	if stream.Kind != StreamKindFrameRef {
		return "", false, nil
	}
	var status string
	if err := conn.QueryRowContext(ctx, `SELECT status FROM frames WHERE id=?`, stream.FrameID).Scan(&status); err != nil {
		return "", false, err
	}
	status = normalizeTerminalStatus(status)
	return status, terminalStatus(status), nil
}

func terminalFrameRunnerDetail(status string) string {
	switch normalizeTerminalStatus(status) {
	case "completed":
		return "The task completed under the authoritative Frame lifecycle."
	case "cancelled":
		return "The task was cancelled under the authoritative Frame lifecycle."
	default:
		return "The task failed under the authoritative Frame lifecycle."
	}
}
