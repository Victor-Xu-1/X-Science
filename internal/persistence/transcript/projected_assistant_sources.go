package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type ProjectedAssistantSourceRange struct {
	FirstPublicationSequence int64
	LastPublicationSequence  int64
}

// These are read-only source events, not a persisted rendering permission.
type ProjectedAssistantSource struct {
	RecordIndex          int
	Assistant            Event
	Terminal             Event
	RetainedAfterFailure bool
}

// ListProjectedAssistantSources reads only the requested page ranges. A real
// completed or retained-final receipt and the branch's last assistant segment must close each
// range; arbitrary progress, imported bodies and ID-looking text cannot qualify.
func (r *Repository) ListProjectedAssistantSources(ctx context.Context, owner string, expected ProjectionSnapshot, ranges []ProjectedAssistantSourceRange) ([]ProjectedAssistantSource, error) {
	db := r.readDatabase()
	if db == nil {
		return nil, ErrSchemaUnavailable
	}
	if strings.TrimSpace(owner) == "" || expected.StreamUID == "" || !validTranscriptBranchID(expected.BranchID) || expected.BranchGeneration <= 0 || expected.ThroughPublicationSequence <= 0 || len(ranges) > maxProjectedEvents {
		return nil, ErrBranchStateStale
	}
	if len(ranges) == 0 {
		return nil, nil
	}
	values, args := make([]string, len(ranges)), make([]any, 0, 3*len(ranges)+3)
	for index, span := range ranges {
		if span.FirstPublicationSequence <= 0 || span.LastPublicationSequence < span.FirstPublicationSequence || span.LastPublicationSequence > expected.ThroughPublicationSequence {
			return nil, ErrBranchStateStale
		}
		values[index] = "(?,?,?)"
		args = append(args, index, span.FirstPublicationSequence, span.LastPublicationSequence)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, schemaError(err)
	}
	defer func() { _ = tx.Rollback() }()
	kind, _, actual, err := projectionSnapshotForBranchTx(ctx, tx, expected.StreamUID, owner, expected.BranchID)
	if err != nil {
		return nil, schemaError(err)
	}
	if kind != string(StreamKindFrameRef) || actual.BranchGeneration != expected.BranchGeneration || expected.ThroughPublicationSequence > actual.ThroughPublicationSequence {
		return nil, ErrBranchStateStale
	}
	args = append(args, expected.StreamUID, expected.BranchID, expected.BranchID)
	rows, err := tx.QueryContext(ctx, `WITH requested(record_index,first_seq,last_seq) AS (VALUES `+strings.Join(values, ",")+`)
		SELECT requested.record_index,a.event_id,a.publication_seq,a.client_message_id,a.runner_attempt,a.payload_json,a.created_at,
			terminal.event_id,terminal.publication_seq,terminal.client_message_id,terminal.payload_json,terminal.created_at,receipt.status
		FROM requested
		JOIN transcript_events a ON a.stream_uid=? AND a.publication_seq>=requested.first_seq AND a.publication_seq<=requested.last_seq
		JOIN transcript_branch_events am ON am.stream_uid=a.stream_uid AND am.event_id=a.event_id AND am.branch_id=?
		JOIN transcript_runner_attempts attempt ON attempt.stream_uid=a.stream_uid AND attempt.attempt=a.runner_attempt
		JOIN transcript_runner_receipts receipt ON receipt.stream_uid=a.stream_uid AND receipt.attempt=a.runner_attempt
		JOIN transcript_events terminal ON terminal.stream_uid=a.stream_uid AND terminal.event_id=receipt.event_id AND terminal.runner_attempt=a.runner_attempt
		JOIN transcript_branch_events tm ON tm.stream_uid=terminal.stream_uid AND tm.event_id=terminal.event_id AND tm.branch_id=?
		WHERE a.event_type='assistant_message' AND a.source='payload' AND a.runner_attempt>0
			AND terminal.event_type='runner_finished' AND terminal.source='payload'
			AND (receipt.status='completed' OR (receipt.status='failed'
				AND json_extract(terminal.payload_json,'$.retained_final_response_id')=a.client_message_id
				AND (json_extract(terminal.payload_json,'$.reason_code')=? OR
					(json_extract(terminal.payload_json,'$.reason_code') IN ('runner_execution_failed','runner_stop_hook_failed')
					AND json_extract(terminal.payload_json,'$.failure_stage')='artifact_evidence'))))
			AND terminal.publication_seq=requested.last_seq AND a.publication_seq<terminal.publication_seq
			AND (receipt.status='failed' OR NOT EXISTS(SELECT 1 FROM transcript_runner_attempts later WHERE later.stream_uid=a.stream_uid
				AND later.attempt>a.runner_attempt AND later.claimed_input_revision=attempt.claimed_input_revision))
			AND NOT EXISTS(SELECT 1 FROM transcript_events later JOIN transcript_branch_events lm
				ON lm.stream_uid=later.stream_uid AND lm.event_id=later.event_id AND lm.branch_id=am.branch_id
				WHERE later.stream_uid=a.stream_uid AND later.runner_attempt=a.runner_attempt AND later.event_type='assistant_message'
				AND later.publication_seq>a.publication_seq AND later.publication_seq<terminal.publication_seq)
		ORDER BY requested.record_index,a.publication_seq`, append(args, RunnerCleanupFailedReason)...)
	if err != nil {
		return nil, schemaError(err)
	}
	var sources []ProjectedAssistantSource
	for rows.Next() {
		var source ProjectedAssistantSource
		var attempt int64
		var status string
		if err := rows.Scan(&source.RecordIndex, &source.Assistant.EventID, &source.Assistant.PublicationSeq, &source.Assistant.ClientMessageID,
			&attempt, &source.Assistant.PayloadJSON, &source.Assistant.CreatedAt, &source.Terminal.EventID, &source.Terminal.PublicationSeq,
			&source.Terminal.ClientMessageID, &source.Terminal.PayloadJSON, &source.Terminal.CreatedAt, &status); err != nil {
			_ = rows.Close()
			return nil, schemaError(err)
		}
		if _, _, err := terminalPayloadDetail(source.Terminal.PayloadJSON, status); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var assistant, terminal map[string]any
		if json.Unmarshal(source.Assistant.PayloadJSON, &assistant) != nil || json.Unmarshal(source.Terminal.PayloadJSON, &terminal) != nil {
			_ = rows.Close()
			return nil, ErrEventConflict
		}
		if status == "failed" {
			source.RetainedAfterFailure = RetainedFinalResponseID(terminal) == source.Assistant.ClientMessageID &&
				matchesRetainedFinalResponse(terminal, source.Assistant.PayloadJSON)
			if !source.RetainedAfterFailure {
				continue
			}
		}
		left, leftPresent, leftErr := ParseAssistantSegmentV1(assistant)
		right, rightPresent, rightErr := ParseAssistantSegmentV1(terminal)
		if leftErr != nil || rightErr != nil || !leftPresent || !rightPresent || left.Ordinal != right.Ordinal || IsContextCompactionTerminal(terminal) {
			continue
		}
		source.Assistant.StreamUID, source.Assistant.Type, source.Assistant.Source, source.Assistant.RunnerAttempt = expected.StreamUID, "assistant_message", EventSourcePayload, &attempt
		source.Terminal.StreamUID, source.Terminal.Type, source.Terminal.Source, source.Terminal.RunnerAttempt = expected.StreamUID, "runner_finished", EventSourcePayload, &attempt
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, schemaError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, schemaError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, schemaError(err)
	}
	return sources, nil
}
