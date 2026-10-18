package transcript

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// CancelExactFrameRunner consumes the caller's frozen canonical coordinates.
// It cannot select a different higher-epoch stream or the previous task's
// terminal attempt when the new input has not yet been claimed.
func (tx *ImmediateTransaction) CancelExactFrameRunner(ctx context.Context, owner, frame, uid string, epoch, revision, attempt int64, reason string, destinations []string) (CancelRunnerResult, error) {
	if tx == nil || tx.conn == nil || revision <= 0 || epoch <= 0 || attempt < 0 {
		return CancelRunnerResult{}, ErrClaimStale
	}
	stream, err := getStreamConn(ctx, tx.conn, uid, owner)
	if err != nil {
		return CancelRunnerResult{}, err
	}
	if stream.Kind != StreamKindFrameRef || stream.FrameID != frame || stream.Epoch != epoch || stream.InputRevision != revision {
		return CancelRunnerResult{}, ErrClaimStale
	}
	var actualAttempt, claimedRevision int64
	var status string
	err = tx.conn.QueryRowContext(ctx, "SELECT attempt,claimed_input_revision,status FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1", uid).Scan(&actualAttempt, &claimedRevision, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CancelRunnerResult{}, err
	}
	if attempt == 0 {
		if status == "running" || claimedRevision >= revision || stream.ConsumedInputRevision >= revision {
			return CancelRunnerResult{}, ErrClaimStale
		}
		if err := tx.createCancellationAttempt(ctx, uid, revision, actualAttempt+1); err != nil {
			return CancelRunnerResult{}, err
		}
	} else if actualAttempt != attempt || claimedRevision != revision {
		return CancelRunnerResult{}, ErrClaimStale
	}
	return tx.cancelFrameRunner(ctx, uid, revision, owner, reason, destinations)
}

// Both existing tree cancellation and exact scoped cancellation use the same
// cancellation receipt writer, including the existing cancel-before-claim path.
func (tx *ImmediateTransaction) cancelFrameRunner(ctx context.Context, streamUID string, inputRevision int64, ownerID, reasonCode string, destinations []string) (CancelRunnerResult, error) {
	var attempt int64
	err := tx.conn.QueryRowContext(ctx, "SELECT attempt FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1", streamUID).Scan(&attempt)
	if errors.Is(err, sql.ErrNoRows) {
		attempt = 1
		err = tx.createCancellationAttempt(ctx, streamUID, inputRevision, attempt)
	}
	if err != nil {
		return CancelRunnerResult{}, err
	}
	input := CancelRunnerInput{StreamUID: streamUID, OwnerID: ownerID, ExpectedAttempt: attempt,
		ClientMessageID: "runner-cancel:" + streamUID + ":" + strconv.FormatInt(attempt, 10),
		ReasonCode:      reasonCode, Destinations: destinations}
	if err := normalizeCancelRunnerInput(&input); err != nil {
		return CancelRunnerResult{}, err
	}
	return cancelRunnerConn(ctx, tx.conn, tx.repository.now().UTC(), input)
}

func (tx *ImmediateTransaction) createCancellationAttempt(ctx context.Context, streamUID string, inputRevision, attempt int64) error {
	_, digest, err := tx.repository.newClaimToken()
	if err != nil {
		return err
	}
	now := tx.repository.now().UTC()
	_, err = tx.conn.ExecContext(ctx, `INSERT INTO transcript_runner_attempts(
  stream_uid,attempt,runner_id,claim_token_sha256,claimed_input_revision,
  resume_source,resume_checkpoint_sequence,status,phase,phase_sequence,
  last_checkpoint_sequence,claimed_at,expires_at
 ) VALUES(?,?,?,?,?,'fresh',0,'running','claimed',1,0,?,?)`,
		streamUID, attempt, "cancel-before-claim", digest, inputRevision, now, now)
	return err
}
