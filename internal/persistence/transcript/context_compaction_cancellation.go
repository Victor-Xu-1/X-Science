package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func readCancelledContextCompactionRequest(ctx context.Context, conn *sql.Conn, owner, frame, id string, expected ContextCompactionTarget) (ContextCompactionRequest, error) {
	var raw []byte
	err := conn.QueryRowContext(ctx, `SELECT payload FROM queued_user_messages WHERE frame_id=? AND intent_id=? AND state='retracted'`, frame, "branch-continue:"+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextCompactionRequest{}, ErrContextCompactionNotFound
	}
	if err != nil {
		return ContextCompactionRequest{}, err
	}
	var envelope struct {
		Delivery struct {
			Request ContextCompactionRequest `json:"context_compaction_request"`
		} `json:"delivery"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Delivery.Request.Validate() != nil {
		return ContextCompactionRequest{}, ErrContextCompactionConflict
	}
	request := envelope.Delivery.Request
	if request.RequestID != id || !sameContextCompactionIdentity(expected, request.Expected) {
		return request, ErrContextCompactionStale
	}
	if _, found, err := ReadFrameMessageIntentCancellation(ctx, conn, request.MessageIntent(owner, frame)); err != nil {
		return request, err
	} else if !found {
		return request, ErrContextCompactionConflict
	}
	return request, nil
}

// The workspace caller persists the existing queue cancellation envelope in
// this same transaction. No hierarchy, kernel or external job is traversed.
func (tx *ImmediateTransaction) CancelContextCompaction(ctx context.Context, owner, frame string, request ContextCompactionRequest) (ContextCompactionReceipt, error) {
	if err := request.Validate(); err != nil {
		return ContextCompactionReceipt{}, err
	}
	result, err := contextCompactionReceiptConn(ctx, tx.conn, owner, frame, request, tx.repository.now().UTC())
	if err != nil && !errors.Is(err, ErrContextCompactionNotFound) {
		return result, err
	}
	if err == nil && (result.State == "committed" || result.State == "failed" || result.State == "cancelled") {
		result.Replayed = true
		if result.State != "cancelled" {
			result.CancellationStatus = "already_terminal"
		}
		return result, nil
	}
	var reclaimed bool
	if result.Accepted && result.Attempt > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT status='reclaimed' FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`,
			request.Expected.StreamUID, result.Attempt).Scan(&reclaimed); err != nil {
			return result, err
		}
	}
	if result.Accepted && result.Attempt > 0 && !reclaimed {
		input := CancelRunnerInput{StreamUID: request.Expected.StreamUID, OwnerID: owner, ExpectedAttempt: result.Attempt,
			ClientMessageID: "context-compaction-cancel:" + request.RequestID, ReasonCode: "manual_context_compaction_cancelled", Destinations: []string{"ws"}, contextCompaction: &request}
		cancelled, err := cancelRunnerConn(ctx, tx.conn, tx.repository.now().UTC(), input)
		if err != nil {
			return result, err
		}
		if cancelled.CurrentStatus != "cancelled" {
			return result, ErrContextCompactionConflict
		}
		result.TerminalEventID = cancelled.Receipt.EventID
	} else if result.Accepted {
		if _, err := tx.ExecContext(ctx, `UPDATE transcript_streams SET consumed_input_revision=MAX(consumed_input_revision,?) WHERE stream_uid=?`, result.InputRevision, request.Expected.StreamUID); err != nil {
			return result, err
		}
		if err := restoreContextCompactionFrameConn(ctx, tx.conn, owner, frame, request, result.InputRevision); err != nil {
			return result, err
		}
	}
	result.State = "cancelled"
	result.CancellationStatus = "cancelled"
	if !result.Accepted {
		result.CancellationStatus = "cancelled_before_acceptance"
		result.ExecutionObservation = "not_started"
		result.CanSubmitNextInput = true
	}
	return result, nil
}

func validateContextCompactionAttemptConn(ctx context.Context, conn *sql.Conn, stream Stream, attempt, revision int64, request ContextCompactionRequest) error {
	current, _, _, err := contextCompactionTargetConn(ctx, conn, stream.OwnerID, stream.FrameID)
	if err != nil {
		return err
	}
	if !sameContextCompactionIdentity(current, request.Expected) {
		return ErrContextCompactionStale
	}
	accepted, found, err := ReadAcceptedFrameMessageSnapshot(ctx, conn, request.MessageIntent(stream.OwnerID, stream.FrameID))
	if err != nil {
		return err
	}
	if !found || accepted.InputRevision != revision {
		return ErrContextCompactionConflict
	}
	var actual int64
	if err := conn.QueryRowContext(ctx, `SELECT claimed_input_revision FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`, stream.UID, attempt).Scan(&actual); err != nil {
		return err
	}
	if actual != revision {
		return ErrContextCompactionConflict
	}
	return nil
}

func restoreContextCompactionFrameConn(ctx context.Context, conn *sql.Conn, owner, frame string, request ContextCompactionRequest, revision int64) error {
	intent := request.MessageIntent(owner, frame)
	accepted, found, err := ReadAcceptedFrameMessageSnapshot(ctx, conn, intent)
	if err != nil {
		return err
	}
	if !found {
		return ErrContextCompactionNotFound
	}
	var payload struct {
		Input map[string]any `json:"inputData"`
	}
	if json.Unmarshal(accepted.PayloadJSON, &payload) != nil {
		return ErrContextCompactionConflict
	}
	previous, _ := payload.Input["context_compaction_previous_status"].(string)
	if previous == "" {
		return ErrContextCompactionConflict
	}
	_, err = conn.ExecContext(ctx, `UPDATE frames SET status=? WHERE id=? AND incarnation_id=?
		AND NOT EXISTS(SELECT 1 FROM transcript_streams WHERE stream_uid=? AND input_revision>?)`,
		previous, frame, request.Expected.FrameIncarnationID, request.Expected.StreamUID, revision)
	return err
}

func contextCompactionTerminalPayload(request ContextCompactionRequest, status string) []byte {
	raw, _ := json.Marshal(map[string]any{"status": status, "purpose": ContextCompactionPurpose, "maintenance": request})
	return raw
}
