package transcript

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Both readers supply one read_branch scope and a selected event-ID relation.
// Replay leaves the cutoff NULL to retain its existing active-branch closure;
// a page binds the validated requested branch and its fixed publication cutoff.
// A historical terminal batch has read authority, never renewed execution authority.
const canonicalToolBatchReadCTE = `eligible_tool_batches(
	stream_uid,batch_id,source_event_id,call_count,state,branch_id,through_publication_seq
) AS (
	SELECT batch.stream_uid,batch.batch_id,batch.source_event_id,batch.call_count,batch.state,
		batch.branch_id,branch.through_publication_seq
	FROM transcript_tool_call_batches batch
	JOIN read_branch branch ON branch.stream_uid=batch.stream_uid AND branch.branch_id=batch.branch_id
		AND (branch.generation=batch.branch_generation OR
			(batch.branch_generation<branch.generation AND batch.state IN ('settled','cancelled','outcome_unknown')))
	JOIN transcript_branch_events source ON source.stream_uid=batch.stream_uid
		AND source.branch_id=batch.branch_id AND source.event_id=batch.source_event_id
	JOIN transcript_events source_event ON source_event.stream_uid=source.stream_uid
		AND source_event.event_id=source.event_id
	WHERE branch.through_publication_seq IS NULL OR source_event.publication_seq<=branch.through_publication_seq
)`

// Count every eligible relation for each selected terminal. Reusable call IDs,
// payload lookalikes and ambiguous origins cannot supply an arbitrary winner or
// multiply the caller's already bounded page/replay rows.
const canonicalToolOutcomeReadCTE = `canonical_tool_outcomes(
	stream_uid,terminal_event_id,batch_id,source_event_id,ordinal,tool_call_id
) AS (
	SELECT item.stream_uid,item.terminal_event_id,
		MIN(batch.batch_id),MIN(batch.source_event_id),MIN(item.ordinal),MIN(item.tool_call_id)
	FROM eligible_tool_batches batch
	JOIN transcript_tool_call_items item ON item.batch_id=batch.batch_id AND item.stream_uid=batch.stream_uid
	JOIN selected ON selected.event_id=item.terminal_event_id
	JOIN transcript_branch_events terminal ON terminal.stream_uid=item.stream_uid
		AND terminal.branch_id=batch.branch_id AND terminal.event_id=item.terminal_event_id
	JOIN transcript_events terminal_event ON terminal_event.stream_uid=terminal.stream_uid
		AND terminal_event.event_id=terminal.event_id
	WHERE item.terminal_event_id IS NOT NULL
		AND (batch.through_publication_seq IS NULL OR terminal_event.publication_seq<=batch.through_publication_seq)
	GROUP BY item.stream_uid,item.terminal_event_id
	HAVING COUNT(*)=1
)`

func projectedToolOutcomesTx(
	ctx context.Context, tx *sql.Tx, input ListProjectedEventsInput, snapshot ProjectionSnapshot, pageEventIDs []int64,
) (map[int64]*RunnerReplayToolOutcome, error) {
	hasAuthority, err := hasCanonicalToolBatchAuthority(ctx, tx)
	if err != nil {
		return nil, err
	}
	outcomes := make(map[int64]*RunnerReplayToolOutcome)
	if !hasAuthority || len(pageEventIDs) == 0 {
		return outcomes, nil
	}
	through := input.ThroughPublicationSequence
	if through == 0 {
		through = snapshot.ThroughPublicationSequence
	}
	arguments := make([]any, 0, 4+len(pageEventIDs))
	arguments = append(arguments, snapshot.StreamUID, snapshot.BranchID, snapshot.BranchGeneration, through)
	for _, eventID := range pageEventIDs {
		arguments = append(arguments, eventID)
	}
	pageValues := strings.TrimSuffix(strings.Repeat("(?),", len(pageEventIDs)), ",")
	rows, err := tx.QueryContext(ctx, `WITH
		read_branch(stream_uid,branch_id,generation,through_publication_seq) AS (VALUES(?,?,?,?)),
		selected(event_id) AS (VALUES `+pageValues+`),
		`+canonicalToolBatchReadCTE+`,
		`+canonicalToolOutcomeReadCTE+`
		SELECT batch_id,source_event_id,ordinal,tool_call_id,terminal_event_id
		FROM canonical_tool_outcomes`, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var batchID, toolCallID sql.NullString
		var sourceEventID, ordinal, terminalEventID sql.NullInt64
		if err := rows.Scan(&batchID, &sourceEventID, &ordinal, &toolCallID, &terminalEventID); err != nil {
			return nil, err
		}
		if outcome := decodeCanonicalToolOutcome(batchID, sourceEventID, ordinal, toolCallID, terminalEventID); outcome != nil {
			outcomes[terminalEventID.Int64] = outcome
		}
	}
	return outcomes, rows.Err()
}

func hasCanonicalToolBatchAuthority(ctx context.Context, tx *sql.Tx) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE type='table' AND name IN ('transcript_tool_call_batches','transcript_tool_call_items')`).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	if count != 2 {
		return false, errors.New("transcript tool call batch authority is incomplete")
	}
	return true, nil
}

func decodeCanonicalToolOutcome(
	batchID sql.NullString, sourceEventID, ordinal sql.NullInt64, toolCallID sql.NullString, terminalEventID sql.NullInt64,
) *RunnerReplayToolOutcome {
	if !batchID.Valid || batchID.String == "" || !toolCallID.Valid || toolCallID.String == "" ||
		!sourceEventID.Valid || sourceEventID.Int64 <= 0 || !ordinal.Valid || ordinal.Int64 < 0 ||
		!terminalEventID.Valid || terminalEventID.Int64 <= sourceEventID.Int64 {
		return nil
	}
	return &RunnerReplayToolOutcome{
		BatchID: batchID.String, SourceEventID: sourceEventID.Int64, Ordinal: ordinal.Int64,
		ToolCallID: toolCallID.String, TerminalEventID: terminalEventID.Int64,
	}
}
