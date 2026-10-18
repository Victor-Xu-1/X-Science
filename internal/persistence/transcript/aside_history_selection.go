package transcript

import (
	"context"
	"errors"
	"strings"
)

// An Aside copies one branch snapshot without changing the parent's active
// branch. Count and clone borrow the same BEGIN IMMEDIATE transaction.
type AsideHistorySelection struct {
	BranchID           string
	Generation         int64
	ThroughPublication int64
}

func (tx *ImmediateTransaction) ResolveAsideHistorySelection(ctx context.Context, streamUID, ownerID, branchID string, generation int64) (AsideHistorySelection, error) {
	if tx == nil || tx.conn == nil || tx.repository == nil {
		return AsideHistorySelection{}, ErrSchemaUnavailable
	}
	branchID = strings.TrimSpace(branchID)
	if (branchID == "") != (generation == 0) || generation < 0 || branchID != "" && !validTranscriptBranchID(branchID) {
		return AsideHistorySelection{}, ErrBranchRequestInvalid
	}
	stream, err := getStreamConn(ctx, tx.conn, streamUID, ownerID)
	if err != nil {
		return AsideHistorySelection{}, err
	}
	if err := validateAsideHistorySourceConn(ctx, tx, stream); err != nil {
		return AsideHistorySelection{}, err
	}
	var active string
	var current int64
	if err := tx.conn.QueryRowContext(ctx, `SELECT active_branch_id,generation FROM transcript_branch_state WHERE stream_uid=?`, stream.UID).Scan(&active, &current); err != nil {
		return AsideHistorySelection{}, err
	}
	if generation != 0 && generation != current {
		return AsideHistorySelection{}, ErrBranchStateStale
	}
	if branchID == "" {
		branchID = active
	}
	var exists bool
	if err := tx.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_branches WHERE stream_uid=? AND branch_id=?)`, stream.UID, branchID).Scan(&exists); err != nil {
		return AsideHistorySelection{}, err
	}
	if !exists {
		return AsideHistorySelection{}, ErrBranchTargetNotFound
	}
	if current <= 0 || stream.NextPublication <= 0 {
		return AsideHistorySelection{}, errors.New("aside branch snapshot is unavailable")
	}
	return AsideHistorySelection{BranchID: branchID, Generation: current, ThroughPublication: stream.NextPublication - 1}, nil
}
