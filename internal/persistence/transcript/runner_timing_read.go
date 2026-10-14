package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type runnerTimingAttempt struct {
	observationErr                     error
	attempt, revision, finishedEventID int64
	status, generation                 string
	claimedAt, expiresAt               time.Time
	finishedAt                         *time.Time
	legacyResume                       bool
	facts                              []runnerTimingFactRecord
}

type runnerTimingFactRecord struct {
	observationErr error
	eventID        int64
	valid          bool
	fact           runnerTimingResumeFact
}

// Range lookup uses the existing (stream_uid, client_message_id) unique index,
// then resolves only those few facts into the current canonical branch. It does
// not repeatedly scan every tool/message payload of a long-running task.
const runnerTimingFactsQuery = `WITH timing_events AS MATERIALIZED (
	SELECT event_id,runner_attempt,client_message_id,source,created_at,payload_json,publication_seq,stream_uid
	FROM transcript_events WHERE stream_uid=? AND client_message_id>=? AND client_message_id<? AND event_type=?
)
SELECT event.event_id,event.runner_attempt,event.client_message_id,event.source,event.created_at,event.payload_json
FROM timing_events event
JOIN transcript_branch_state branch ON branch.stream_uid=event.stream_uid
JOIN transcript_branch_events membership ON membership.stream_uid=event.stream_uid
	AND membership.branch_id=branch.active_branch_id AND membership.event_id=event.event_id
ORDER BY event.publication_seq`

// GetRunnerTimingSummaries reads both timing scopes in one SQL snapshot and
// applies one interval reducer. APIs needing both must use this paired read.
// Attempt rows are closed before the fact query, including on single-connection
// stores. A concurrent successful resume cannot produce a new row/old facts mix.
func (r *Repository) GetRunnerTimingSummaries(ctx context.Context, streamUID, ownerID string) (RunnerTimingSummary, RunnerTaskTimingSummary, bool, error) {
	db := r.readDatabase()
	streamUID, ownerID = strings.TrimSpace(streamUID), strings.TrimSpace(ownerID)
	if db == nil || streamUID == "" || ownerID == "" {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, errors.New("stream and owner are required")
	}
	if _, err := r.GetStream(ctx, streamUID, ownerID); err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	var storedOwner, kind, frameID, projectID, frameStatus string
	var inputRevision int64
	var updatedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,input_revision,updated_at,kind,COALESCE(frame_id,''),COALESCE(project_id,'') FROM transcript_streams WHERE stream_uid=?`, streamUID).Scan(&storedOwner, &inputRevision, &updatedAt, &kind, &frameID, &projectID); err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
	}
	if storedOwner != ownerID {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, ErrOwnerMismatch
	}
	if StreamKind(kind) == StreamKindFrameRef {
		if err := tx.QueryRowContext(ctx, `SELECT status FROM frames WHERE id=? AND project_id=?`, frameID, projectID).Scan(&frameStatus); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
		}
	}
	observedAt := r.now().UTC()
	rows, err := tx.QueryContext(ctx, `
		SELECT attempt,claimed_input_revision,status,claimed_at,expires_at,finished_event_id,finished_at,claim_token_sha256,
			resume_source,COALESCE((SELECT runner_attempt FROM transcript_runner_checkpoints checkpoint
				WHERE checkpoint.stream_uid=attempt.stream_uid AND checkpoint.checkpoint_sequence=attempt.resume_checkpoint_sequence),0)
		FROM transcript_runner_attempts attempt WHERE stream_uid=? ORDER BY attempt`, streamUID)
	if err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
	}
	var attempts []runnerTimingAttempt
	indices := map[int64]int{}
	for rows.Next() {
		var item runnerTimingAttempt
		var terminalID sql.NullInt64
		var claimedAt, expiresAt, terminalAt any
		var digest []byte
		var resumeSource string
		var parentAttempt int64
		if err := rows.Scan(&item.attempt, &item.revision, &item.status, &claimedAt, &expiresAt, &terminalID, &terminalAt, &digest, &resumeSource, &parentAttempt); err != nil {
			_ = rows.Close()
			return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
		}
		var claimedErr, expiresErr error
		item.claimedAt, _, claimedErr = runnerTimingTimeValue(claimedAt, true)
		item.expiresAt, _, expiresErr = runnerTimingTimeValue(expiresAt, true)
		finished, finishedValid, finishedErr := runnerTimingTimeValue(terminalAt, false)
		item.observationErr = errors.Join(claimedErr, expiresErr, finishedErr)
		item.generation = runnerTimingGeneration(ownerID, streamUID, item.attempt, digest)
		item.legacyResume = resumeSource == string(ResumeSourceCheckpoint) && parentAttempt == item.attempt
		if terminalID.Valid {
			item.finishedEventID = terminalID.Int64
		}
		if finishedValid {
			item.finishedAt = &finished
		}
		indices[item.attempt] = len(attempts)
		attempts = append(attempts, item)
	}
	readErr, closeErr := rows.Err(), rows.Close()
	if readErr != nil || closeErr != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(errors.Join(readErr, closeErr))
	}
	if len(attempts) == 0 {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, nil
	}
	facts, err := tx.QueryContext(ctx, runnerTimingFactsQuery, streamUID, runnerTimingResumeClientPrefix, "runner-timing-resume;", RunnerTimingResumeEventType)
	if err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
	}
	for facts.Next() {
		var record runnerTimingFactRecord
		var attempt sql.NullInt64
		var clientID, source string
		var createdRaw any
		var raw []byte
		if err := facts.Scan(&record.eventID, &attempt, &clientID, &source, &createdRaw, &raw); err != nil {
			_ = facts.Close()
			return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
		}
		index, found := indices[attempt.Int64]
		if !attempt.Valid || !found {
			continue
		}
		fact := &record.fact
		createdAt, _, createdErr := runnerTimingTimeValue(createdRaw, true)
		record.observationErr = createdErr
		record.valid = createdErr == nil && json.Unmarshal(raw, fact) == nil && fact.Schema == runnerTimingResumeSchema &&
			fact.OwnerID == ownerID && fact.StreamUID == streamUID && fact.Attempt == attempt.Int64 &&
			clientID == runnerTimingResumeClientPrefix+fact.Generation && source == string(EventSourcePayload) && fact.ResumedAt.Equal(createdAt)
		attempts[index].facts = append(attempts[index].facts, record)
	}
	readErr, closeErr = facts.Err(), facts.Close()
	if readErr != nil || closeErr != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(errors.Join(readErr, closeErr))
	}
	if err := tx.Commit(); err != nil {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, schemaError(err)
	}
	latest, task, found, err := reduceRunnerTiming(streamUID, attempts, observedAt)
	latest.SnapshotInputRevision, latest.SnapshotUpdatedAt = inputRevision, updatedAt.UTC()
	latest.SnapshotFrameStatus = frameStatus
	return latest, task, found, err
}

func runnerTimingTimeValue(raw any, required bool) (time.Time, bool, error) {
	var value sql.NullTime
	if err := value.Scan(raw); err != nil {
		return time.Time{}, false, errors.Join(ErrEventConflict, err)
	}
	if required && !value.Valid || value.Valid && value.Time.IsZero() {
		return time.Time{}, false, ErrEventConflict
	}
	return value.Time.UTC(), value.Valid, nil
}
