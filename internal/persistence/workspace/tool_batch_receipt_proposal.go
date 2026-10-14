package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// ToolCallProposalForTerminalReceipt resolves an exact existing terminal
// receipt, never the latest use of a provider call ID. The existing unique
// (stream_uid, terminal_event_id) index bounds this lookup. Both coordinates
// must belong to the supplied active canonical branch snapshot.
func lookupToolCallReceiptProposal(ctx context.Context, db *sql.DB, ownerID, callID, toolName string, eventID int64, snapshot transcriptstore.ProjectionSnapshot) (int64, bool, error) {
	if db == nil || ctx == nil || strings.TrimSpace(ownerID) == "" || strings.TrimSpace(callID) == "" || strings.TrimSpace(toolName) == "" || eventID <= 0 || snapshot.StreamUID == "" || snapshot.BranchID == "" || snapshot.BranchGeneration <= 0 || snapshot.ThroughPublicationSequence <= 0 {
		return 0, false, errors.New("exact tool terminal receipt and canonical branch authority are required")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT batch.source_event_id
		FROM transcript_tool_call_items item
		JOIN transcript_tool_call_batches batch ON batch.batch_id=item.batch_id AND batch.stream_uid=item.stream_uid
		JOIN transcript_branch_state state ON state.stream_uid=item.stream_uid
		JOIN transcript_streams stream ON stream.stream_uid=item.stream_uid
		JOIN transcript_branch_events source ON source.stream_uid=batch.stream_uid AND source.branch_id=state.active_branch_id AND source.event_id=batch.source_event_id
		JOIN transcript_branch_events terminal ON terminal.stream_uid=item.stream_uid AND terminal.branch_id=state.active_branch_id AND terminal.event_id=item.terminal_event_id
		JOIN transcript_events source_event ON source_event.stream_uid=item.stream_uid AND source_event.event_id=batch.source_event_id
		JOIN transcript_events terminal_event ON terminal_event.stream_uid=item.stream_uid AND terminal_event.event_id=item.terminal_event_id
		WHERE item.stream_uid=? AND item.terminal_event_id=? AND item.tool_call_id=? AND item.tool_name=?
			AND item.state='completed' AND batch.owner_user_id=? AND stream.owner_id=?
			AND state.active_branch_id=? AND state.generation=? AND source.ordinal<terminal.ordinal
			AND source_event.publication_seq<=? AND terminal_event.publication_seq<=?
		LIMIT 2`,
		snapshot.StreamUID, eventID, callID, toolName, ownerID, ownerID, snapshot.BranchID, snapshot.BranchGeneration, snapshot.ThroughPublicationSequence, snapshot.ThroughPublicationSequence)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, false, rows.Err()
	}
	var proposal int64
	if err := rows.Scan(&proposal); err != nil {
		return 0, false, err
	}
	if rows.Next() {
		return 0, false, nil
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return proposal, proposal > 0, nil
}
