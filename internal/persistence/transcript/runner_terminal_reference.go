package transcript

import "context"

// RunnerTerminalReference carries a real finish result and its original claim
// between internal lifecycle owners. It is not public dispatch metadata.
type RunnerTerminalReference struct {
	Claim   RunnerClaim `json:"-"`
	EventID int64       `json:"-"`
}

// ValidateRecordedRunnerTerminal validates an immutable receipt within the
// caller's settlement transaction. The original finish already committed its
// Frame projection atomically; replay must not overwrite a later continuation.
func (tx *ImmediateTransaction) ValidateRecordedRunnerTerminal(ctx context.Context, reference RunnerTerminalReference, status string) error {
	if tx == nil || tx.repository == nil || tx.conn == nil || reference.EventID <= 0 {
		return ErrClaimStale
	}
	if _, err := validateClaimConn(ctx, tx.conn, reference.Claim, tx.repository.now().UTC(), false); err != nil {
		return err
	}
	var receiptStatus, eventType string
	var eventAttempt int64
	err := tx.conn.QueryRowContext(ctx, `SELECT receipt.status,event.event_type,event.runner_attempt
		FROM transcript_runner_receipts receipt
		JOIN transcript_events event ON event.stream_uid=receipt.stream_uid AND event.event_id=receipt.event_id
		WHERE receipt.stream_uid=? AND receipt.attempt=? AND receipt.event_id=?`,
		reference.Claim.StreamUID, reference.Claim.Attempt, reference.EventID).
		Scan(&receiptStatus, &eventType, &eventAttempt)
	if err != nil {
		return err
	}
	if eventType != "runner_finished" || eventAttempt != reference.Claim.Attempt ||
		!terminalStatus(receiptStatus) || receiptStatus != status {
		return ErrEventConflict
	}
	return nil
}
