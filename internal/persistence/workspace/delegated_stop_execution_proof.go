package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"reflect"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// A post-commit signal must still identify the captured registration's claim,
// not a replacement with the same frame/attempt numbers. The token is compared
// only against its canonical digest and never stored in a Stop receipt.
func (s *Store) DelegatedStopSignalAllowed(ctx context.Context, owner string, receipt DelegatedStopReceipt, target DelegatedStopTarget, claim *transcriptstore.RunnerClaim) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	scope, _, err := delegatedScopeTx(ctx, tx, owner, receipt.Request.Scope.ParentFrameID)
	if err != nil {
		return false, err
	}
	stored, _, found, err := readDelegatedStopTx(ctx, tx, owner, scope, receipt.Request.RequestID)
	if err != nil {
		return false, err
	}
	if !found || !reflect.DeepEqual(stored.Receipt.Request, receipt.Request) {
		return false, ErrDelegatedStopRequestConflict
	}
	selected := false
	for _, outcome := range stored.Receipt.Targets {
		selected = selected || outcome.Target == target && outcome.Outcome != "already_terminal"
	}
	if !selected {
		return false, nil
	}
	args := []any{owner, target.FrameID, target.FrameIncarnationID, scope.RootFrameID, scope.RootFrameIncarnationID,
		target.StreamUID, target.StreamEpoch, target.InputRevision}
	query := `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts attempt
		JOIN transcript_streams stream ON stream.stream_uid=attempt.stream_uid
		JOIN transcript_frame_authority authority ON authority.active_stream_uid=stream.stream_uid AND authority.active_epoch=stream.epoch AND authority.owner_id=stream.owner_id AND authority.session_id=stream.session_id
		JOIN frames frame ON frame.id=stream.frame_id JOIN frames root ON root.id=frame.root_frame_id
		WHERE stream.owner_id=? AND frame.id=? AND frame.incarnation_id=? AND root.id=? AND root.incarnation_id=?
		AND stream.stream_uid=? AND stream.epoch=? AND attempt.claimed_input_revision=? AND attempt.status='cancelled'`
	if claim == nil {
		if target.RunnerAttempt != 0 {
			return false, nil
		}
		query += ` AND attempt.runner_id='cancel-before-claim')`
	} else {
		if claim.StreamUID != target.StreamUID || claim.OwnerID != owner || claim.Attempt != target.RunnerAttempt || claim.ClaimedInputRevision != target.InputRevision {
			return false, nil
		}
		digest := sha256.Sum256([]byte(strings.TrimSpace(claim.ClaimToken)))
		query += ` AND attempt.attempt=? AND attempt.runner_id=? AND attempt.claim_token_sha256=?)`
		args = append(args, claim.Attempt, claim.RunnerID, digest[:])
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, tx.Commit()
}
