package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// An external cancellation owns its own immutable client message ID. A late
// cancellation-only settlement can reuse that terminal, but cannot publish its
// proposed assistant or claim that an unrelated finalization COMMIT succeeded.
func (tx *ImmediateTransaction) inspectCancelledRunnerFinalization(ctx context.Context, input RunnerFinalizationInput) (RunnerFinalizationReceipt, bool, error) {
	if strings.TrimSpace(input.Finish.Status) != "cancelled" || input.Assistant != nil {
		return RunnerFinalizationReceipt{}, false, nil
	}
	claim := input.Finish.Claim
	if _, err := validateClaimConn(ctx, tx.conn, claim, tx.repository.now().UTC(), false); err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	cancellation, err := loadRunnerReceiptConn(ctx, tx.conn, claim.StreamUID, claim.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return RunnerFinalizationReceipt{}, false, nil
	}
	if err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	if cancellation.Status != "cancelled" {
		return RunnerFinalizationReceipt{}, false, nil
	}
	var clientMessageID, status, phase string
	var payload []byte
	var finishedEventID int64
	var finishedAt time.Time
	if err := tx.conn.QueryRowContext(ctx, `
		SELECT event.client_message_id,event.payload_json,
			attempt.status,attempt.phase,attempt.finished_event_id,attempt.finished_at
		FROM transcript_runner_attempts attempt
		JOIN transcript_events event ON event.stream_uid=attempt.stream_uid
			AND event.event_id=attempt.finished_event_id
		WHERE attempt.stream_uid=? AND attempt.attempt=?`, claim.StreamUID, claim.Attempt,
	).Scan(&clientMessageID, &payload, &status, &phase, &finishedEventID, &finishedAt); err != nil {
		return RunnerFinalizationReceipt{}, false, err
	}
	if cancellation.StreamUID != claim.StreamUID || cancellation.Attempt != claim.Attempt ||
		cancellation.EventID <= 0 || cancellation.EventID != finishedEventID ||
		status != cancellation.Status || phase != string(RunnerPhaseTerminal) || !finishedAt.Equal(cancellation.FinishedAt) {
		return RunnerFinalizationReceipt{}, false, ErrEventConflict
	}
	var terminalPayload struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(payload, &terminalPayload) != nil || terminalPayload.Status != cancellation.Status {
		return RunnerFinalizationReceipt{}, false, ErrEventConflict
	}
	// Preserve the caller's exact claim and destinations. The normal inspector
	// checks active stream/branch membership, original terminal identity, receipt
	// and artifact bindings; its proposed-result/unknown-ACK contract is unchanged.
	cancelled := RunnerFinalizationInput{Finish: input.Finish}
	cancelled.Finish.ClientMessageID = clientMessageID
	cancelled.Finish.PayloadJSON = payload
	return tx.InspectRunnerFinalization(ctx, cancelled, nil)
}
