package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

// ToolCallPreExecutionRejection is read from immutable host checkpoints and
// canonical newer user input, never from model arguments. Feedback may be
// absent in legacy checkpoints.
type ToolCallPreExecutionRejection struct {
	Rejected     bool
	FeedbackJSON json.RawMessage
	ToolCallID   string
	ToolName     string
}

// ReadPendingToolCallRejection preserves admission across an approval handoff.
// A rejected call must still be pending and have no evidence of execution.
func (s *Store) ReadPendingToolCallRejection(
	ctx context.Context, claim transcriptstore.RunnerClaim, batchID string, ordinal int64,
) (ToolCallPreExecutionRejection, error) {
	return s.readToolCallRejection(ctx, claim, batchID, ordinal, 0)
}

// ReadTerminalToolCallRejection refines only a legacy orphan-uncertainty receipt.
// It does not mutate/reopen the batch, replace an execution result or grant a
// new call permission. Ambiguous or unrelated outcomes remain unchanged.
func (s *Store) ReadTerminalToolCallRejection(
	ctx context.Context, claim transcriptstore.RunnerClaim, terminalEventID int64,
) (ToolCallPreExecutionRejection, error) {
	if terminalEventID <= 0 {
		return ToolCallPreExecutionRejection{}, ErrToolCallBatchConflict
	}
	return s.readToolCallRejection(ctx, claim, "", 0, terminalEventID)
}

func (s *Store) readToolCallRejection(
	ctx context.Context, claim transcriptstore.RunnerClaim, batchID string, ordinal, terminalEventID int64,
) (ToolCallPreExecutionRejection, error) {
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	var result ToolCallPreExecutionRejection
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var stream transcriptstore.Stream
		if terminalEventID == 0 {
			var err error
			stream, err = tx.ValidateLiveRunnerClaim(ctx, claim)
			if err != nil {
				return err
			}
		}
		if terminalEventID > 0 {
			err := tx.QueryRowContext(ctx, `SELECT item.batch_id,item.ordinal
				FROM transcript_tool_call_items item
				JOIN transcript_tool_call_batches batch ON batch.batch_id=item.batch_id
				JOIN transcript_events event ON event.stream_uid=item.stream_uid AND event.event_id=item.terminal_event_id
				WHERE batch.owner_user_id=? AND batch.stream_uid=? AND item.terminal_event_id=?
					AND batch.state='outcome_unknown' AND item.state='outcome_unknown'
					AND event.event_type='tool_recovery_settlement'
					AND event.source='payload'
					AND json_extract(event.payload_json,'$.reasonCode')='terminal_frame_orphaned_tool'
					AND json_extract(event.payload_json,'$.toolCallId')=item.tool_call_id
					AND json_extract(event.payload_json,'$.toolPhase')='outcome_unknown'`,
				claim.OwnerID, claim.StreamUID, terminalEventID).Scan(&batchID, &ordinal)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		batch, found, err := getToolCallBatchQuery(ctx, tx, batchID)
		if err != nil {
			return err
		}
		if !found || validateToolCallBatchAuthority(ctx, tx, batch, claim) != nil {
			return ErrToolCallBatchConflict
		}
		item, found, err := getToolCallBatchItemQuery(ctx, tx, batchID, ordinal)
		if err != nil {
			return err
		}
		if !found {
			return ErrToolCallBatchConflict
		}
		rejection, err := readCanonicalToolCallRejection(ctx, tx, batch, item)
		if err != nil {
			return err
		}
		newInputRejection := false
		if !rejection.Rejected && terminalEventID == 0 {
			rejection, err = deferredKernelProposalInputRejection(ctx, tx, stream, claim, batch, item)
			if err != nil {
				return err
			}
			newInputRejection = rejection.Rejected
		}
		if !rejection.Rejected {
			return nil
		}
		if item.StartedEventID != 0 || item.WaitingEventID != 0 ||
			(terminalEventID == 0 && (item.State != ToolCallBatchItemStatePending || item.TerminalEventID != 0)) {
			if terminalEventID > 0 || newInputRejection {
				return nil
			}
			return ErrToolCallBatchConflict
		}
		var operations, effects int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_local_operations
			WHERE stream_uid=? AND tool_call_id=?`, batch.StreamUID, item.ToolCallID).Scan(&operations); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_events
			WHERE stream_uid=? AND event_id>? AND event_type='runner_checkpoint'
				AND json_extract(payload_json,'$.toolCallId')=?
				AND json_extract(payload_json,'$.toolPhase') IS NOT NULL
				AND json_extract(payload_json,'$.toolPhase') NOT IN ('prestart_failed','outcome_unknown')`,
			batch.StreamUID, batch.SourceEventID, item.ToolCallID).Scan(&effects); err != nil {
			return err
		}
		if operations != 0 || effects != 0 {
			if terminalEventID > 0 || newInputRejection {
				return nil
			}
			return ErrToolCallBatchConflict
		}
		result = rejection
		return nil
	})
	return result, err
}

func readCanonicalToolCallRejection(
	ctx context.Context, query toolCallBatchQuery, batch ToolCallBatch, item ToolCallBatchItem,
) (ToolCallPreExecutionRejection, error) {
	var event transcriptstore.Event
	var runnerAttempt int64
	var eventType, source string
	err := query.QueryRowContext(ctx, `SELECT event_type,source,publication_seq,runner_attempt,client_message_id,payload_json
		FROM transcript_events WHERE stream_uid=? AND event_id=?`, batch.StreamUID, batch.SourceEventID).Scan(
		&eventType, &source, &event.PublicationSeq, &runnerAttempt, &event.ClientMessageID, &event.PayloadJSON,
	)
	if err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	event.StreamUID, event.EventID, event.RunnerAttempt = batch.StreamUID, batch.SourceEventID, &runnerAttempt
	calls, err := canonicalToolCallBatchItems(event.PayloadJSON)
	if err != nil {
		return ToolCallPreExecutionRejection{}, err
	}
	if eventType != "runner_checkpoint" || source != string(transcriptstore.EventSourcePayload) ||
		!toolCallBatchOriginMatches(batch, event, calls) || item.Ordinal < 0 || item.Ordinal >= int64(len(calls)) {
		return ToolCallPreExecutionRejection{}, ErrToolCallBatchConflict
	}
	call := calls[item.Ordinal]
	if item.ToolCallID != call.ID || item.ToolName != call.Name ||
		!bytes.Equal(item.ArgumentsJSON, call.ArgumentsJSON) || item.ArgumentsSHA256 != call.ArgumentsSHA256 {
		return ToolCallPreExecutionRejection{}, ErrToolCallBatchConflict
	}
	var checkpoint struct {
		Calls []struct {
			Rejected bool            `json:"rejectedBeforeExecution"`
			Feedback json.RawMessage `json:"preExecutionRejection"`
		} `json:"modelToolCalls"`
	}
	if json.Unmarshal(event.PayloadJSON, &checkpoint) != nil || len(checkpoint.Calls) != len(calls) {
		return ToolCallPreExecutionRejection{}, ErrToolCallBatchConflict
	}
	rejection := checkpoint.Calls[item.Ordinal]
	return ToolCallPreExecutionRejection{
		Rejected: rejection.Rejected, FeedbackJSON: append(json.RawMessage(nil), rejection.Feedback...),
		ToolCallID: item.ToolCallID, ToolName: item.ToolName,
	}, nil
}
