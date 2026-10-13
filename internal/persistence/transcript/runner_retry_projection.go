package transcript

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const runnerRetryCheckpointQuery = `
	SELECT event.event_id,checkpoint.runner_attempt,CASE WHEN json_extract(event.payload_json,'$.auto_resume')=1
		THEN COALESCE(json_extract(event.payload_json,'$.reason_code'),json_extract(event.payload_json,'$.reasonCode'),'')
		ELSE '' END
	FROM transcript_runner_checkpoints checkpoint
	JOIN transcript_events event ON event.stream_uid=checkpoint.stream_uid AND event.event_id=checkpoint.event_id
	JOIN transcript_runner_attempts runner ON runner.stream_uid=checkpoint.stream_uid AND runner.attempt=checkpoint.runner_attempt
	WHERE checkpoint.stream_uid=? AND runner.claimed_input_revision=? AND runner.attempt<=?
		AND json_extract(event.payload_json,'$.status')='interrupted'
	ORDER BY checkpoint.checkpoint_sequence DESC LIMIT 1`

const runnerRetryProgressQuery = `SELECT EXISTS (
	SELECT 1 FROM transcript_events event
	WHERE event.stream_uid=? AND event.event_id>? AND event.runner_attempt>=? AND event.runner_attempt<=?
		AND (
			event.event_type IN ('runner_finished','tool_operation_observation')
			OR (event.event_type='runner_checkpoint'
				AND length(trim(COALESCE(json_extract(event.payload_json,'$.toolCallId'),'')))>0
				AND length(trim(COALESCE(json_extract(event.payload_json,'$.toolName'),'')))>0)
			OR (event.event_type IN ('content_delta','content_reset','assistant_message')
				AND COALESCE(json_extract(event.payload_json,'$.visibility'),'')<>'provisional'
				AND COALESCE(json_extract(event.payload_json,'$.block_type'),json_extract(event.payload_json,'$.blockType'),'')<>'thinking'
				AND length(trim(COALESCE(json_extract(event.payload_json,'$.text'),json_extract(event.payload_json,'$.content'),'')))>0)
		))`

// RunnerAutomaticRetryReason projects the latest interrupted generation until
// real public/tool progress supersedes it. Starting another provider request is
// not progress. This read model never changes runner eligibility or scheduling.
// The expected coordinates fence a caller's timing snapshot against new input
// or a newer attempt accepted while that snapshot was being assembled.
func (r *Repository) RunnerAutomaticRetryReason(ctx context.Context, streamUID, ownerID string, inputRevision, attempt int64) (string, error) {
	db := r.readDatabase()
	if db == nil {
		return "", ErrSchemaUnavailable
	}
	if strings.TrimSpace(streamUID) == "" || strings.TrimSpace(ownerID) == "" {
		return "", errors.New("stream and owner are required")
	}
	if inputRevision <= 0 || attempt <= 0 {
		// Accepted input may be visible before any runner has claimed it.
		return "", nil
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	var storedOwner string
	var currentInput int64
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,input_revision FROM transcript_streams WHERE stream_uid=?`, streamUID).Scan(&storedOwner, &currentInput); err != nil {
		return "", schemaError(err)
	}
	if storedOwner != ownerID {
		return "", ErrOwnerMismatch
	}
	if currentInput != inputRevision {
		return "", nil
	}
	var currentAttempt, claimedInput int64
	var finishedEvent sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT attempt,claimed_input_revision,finished_event_id
		FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, streamUID).
		Scan(&currentAttempt, &claimedInput, &finishedEvent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", schemaError(err)
	}
	if currentAttempt != attempt || claimedInput != inputRevision || finishedEvent.Valid {
		return "", nil
	}
	// Start with the checkpoint index, not a sort of all token events. Then
	// inspect only the suffix since that interruption. A delayed background
	// observation from an older attempt cannot clear a newer interruption.
	var reason string
	var retryEvent, retryAttempt int64
	err = tx.QueryRowContext(ctx, runnerRetryCheckpointQuery, streamUID, inputRevision, attempt).
		Scan(&retryEvent, &retryAttempt, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", schemaError(err)
	}
	if strings.TrimSpace(reason) == "" {
		return "", nil
	}
	// Only real public/tool progress or a terminal outcome supersedes the
	// retry. Thinking, empty deltas and bookkeeping do not imply recovery.
	var progressed bool
	err = tx.QueryRowContext(ctx, runnerRetryProgressQuery, streamUID, retryEvent, retryAttempt, attempt).Scan(&progressed)
	if err != nil {
		return "", schemaError(err)
	}
	if err := tx.Commit(); err != nil {
		return "", schemaError(err)
	}
	if progressed {
		return "", nil
	}
	return strings.TrimSpace(reason), nil
}
