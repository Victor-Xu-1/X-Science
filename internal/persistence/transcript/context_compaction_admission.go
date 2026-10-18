package transcript

import (
	"context"
	"encoding/json"
	"errors"
)

// AdmitContextCompaction composes with the workspace's existing durable
// dispatch in one transaction. The canonical input is the sole acceptance.
func (tx *ImmediateTransaction) AdmitContextCompaction(ctx context.Context, owner, frame string, request ContextCompactionRequest) (ContextCompactionReceipt, bool, error) {
	if err := request.Validate(); err != nil {
		return ContextCompactionReceipt{}, false, err
	}
	prior, err := contextCompactionReceiptConn(ctx, tx.conn, owner, frame, request, tx.repository.now().UTC())
	if err == nil {
		if prior.CancellationStatus == "cancelled_before_acceptance" {
			return prior, false, ErrMessageIntentCancelled
		}
		prior.Replayed = true
		return prior, false, nil
	}
	if !errors.Is(err, ErrContextCompactionNotFound) {
		return prior, false, err
	}
	target, stream, status, err := contextCompactionTargetConn(ctx, tx.conn, owner, frame)
	if err != nil {
		return prior, false, err
	}
	if target != request.Expected {
		return prior, false, ErrContextCompactionStale
	}
	availability, err := contextCompactionAvailabilityConn(ctx, tx.conn, stream, status)
	if err != nil {
		return prior, false, err
	}
	if !availability.Available {
		return prior, false, ErrContextCompactionBusy
	}
	intent := request.MessageIntent(owner, frame)
	value, err := json.Marshal(request)
	if err != nil {
		return prior, false, err
	}
	var requestValue map[string]any
	if err = json.Unmarshal(value, &requestValue); err != nil {
		return prior, false, err
	}
	_, _, created, err := tx.AppendFrameUserEvent(ctx, AppendFrameUserEventInput{
		contextCompactionAdmitted: true,
		StreamUID:                 stream.UID, OwnerID: owner, ClientMessageID: intent.InputMessageID, FrameEventID: "context-compaction-input:" + request.RequestID,
		MessageUUID: intent.InputMessageID, Text: "Compact conversation context.", MessageOrigin: "task_intent", MessageIntent: &intent,
		InputData:    map[string]any{"turn_intent": ContextCompactionIntent, "context_compaction": requestValue, "context_compaction_previous_status": status},
		Destinations: []string{"ws"},
	})
	if err != nil {
		return prior, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE frames SET status='processing' WHERE id=?`, frame); err != nil {
		return prior, false, err
	}
	result, err := contextCompactionReceiptConn(ctx, tx.conn, owner, frame, request, tx.repository.now().UTC())
	return result, created, err
}

func (r *Repository) ContextCompactionRequestByID(ctx context.Context, owner, frame, requestID string, expected ContextCompactionTarget) (ContextCompactionRequest, error) {
	if r == nil || r.readDatabase() == nil {
		return ContextCompactionRequest{}, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return ContextCompactionRequest{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return ContextCompactionRequest{}, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
	target, stream, _, err := contextCompactionTargetConn(ctx, conn, owner, frame)
	if err != nil {
		return ContextCompactionRequest{}, err
	}
	if !sameContextCompactionIdentity(target, expected) {
		return ContextCompactionRequest{}, ErrContextCompactionStale
	}
	event, found, err := findEventByClientID(ctx, conn, stream.UID, "branch-continue:"+requestID)
	if err != nil {
		return ContextCompactionRequest{}, err
	}
	if !found {
		return readCancelledContextCompactionRequest(ctx, conn, owner, frame, requestID, expected)
	}
	var payload map[string]any
	if json.Unmarshal(event.PayloadJSON, &payload) != nil {
		return ContextCompactionRequest{}, ErrContextCompactionConflict
	}
	request, err := ContextCompactionRequestFromPayload(payload)
	if err != nil || request == nil || request.RequestID != requestID {
		return ContextCompactionRequest{}, ErrContextCompactionConflict
	}
	if !sameContextCompactionIdentity(request.Expected, expected) {
		return ContextCompactionRequest{}, ErrContextCompactionStale
	}
	return *request, nil
}
