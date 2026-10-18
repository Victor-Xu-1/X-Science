package workspace

import "context"

// Branches may share an inherited invocation. Read branch keys and indexed
// membership for this single source event, never all historical receipt rows.
// A selected historical branch's Stop also fences a delayed admission whose
// invocation is visible there, without forbidding a new explicit host call.
func delegationVisibleFencesTx(ctx context.Context, tx workspaceTransaction, record delegationAdmissionRecord) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT branch.branch_id FROM transcript_branches branch
		WHERE branch.stream_uid=? AND EXISTS(SELECT 1 FROM transcript_branch_events member
		WHERE member.stream_uid=branch.stream_uid AND member.branch_id=branch.branch_id AND member.event_id=?)
		ORDER BY branch.branch_id`, record.StreamUID, record.SourceEventID)
	if err != nil {
		return nil, err
	}
	var branches []string
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			rows.Close()
			return nil, err
		}
		branches = append(branches, branch)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(branches) == 0 {
		return nil, ErrDelegatedStopOriginUnavailable
	}
	result := make(map[string]int64, len(branches))
	for _, branch := range branches {
		fence, _, _, err := delegationFenceTx(ctx, tx, record.OwnerID, record.Scope, record.StreamUID, record.StreamEpoch, branch)
		if err != nil {
			return nil, err
		}
		result[branch] = fence.Generation
	}
	return result, nil
}
