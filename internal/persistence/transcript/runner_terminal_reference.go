package transcript

import "context"

// RunnerTerminalReference carries a real finish result and its original claim
// between internal lifecycle owners. It is not public dispatch metadata.
type RunnerTerminalReference struct {
	Claim   RunnerClaim `json:"-"`
	EventID int64       `json:"-"`
}

// RecoveredRunnerTerminalReference identifies a finish observed after restart.
// It grants no runner mutation authority: a separate owned dispatch must bind
// the runner identity, and settlement revalidates every coordinate in SQLite.
type RecoveredRunnerTerminalReference struct {
	StreamUID string `json:"-"`
	OwnerID   string `json:"-"`
	RunnerID  string `json:"-"`
	Attempt   int64  `json:"-"`
	EventID   int64  `json:"-"`
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
	return tx.validateRecordedRunnerReceipt(ctx, reference.Claim.StreamUID, reference.Claim.Attempt, reference.EventID, status)
}

// ValidateRecoveredRunnerTerminal is only for a caller which already owns the
// dispatch associated with reference.RunnerID in this same transaction.
func (tx *ImmediateTransaction) ValidateRecoveredRunnerTerminal(ctx context.Context, reference RecoveredRunnerTerminalReference, status string) error {
	if tx == nil || tx.repository == nil || tx.conn == nil || reference.RunnerID == "" ||
		reference.Attempt <= 0 || reference.EventID <= 0 {
		return ErrClaimStale
	}
	if _, err := getStreamConn(ctx, tx.conn, reference.StreamUID, reference.OwnerID); err != nil {
		return err
	}
	var attemptStatus string
	var finishedEventID int64
	err := tx.conn.QueryRowContext(ctx, `SELECT status,finished_event_id
		FROM transcript_runner_attempts WHERE stream_uid=? AND runner_id=? AND attempt=?
		AND attempt=(SELECT MAX(attempt) FROM transcript_runner_attempts WHERE stream_uid=? AND runner_id=?)`,
		reference.StreamUID, reference.RunnerID, reference.Attempt, reference.StreamUID, reference.RunnerID).
		Scan(&attemptStatus, &finishedEventID)
	if err != nil {
		return err
	}
	if !terminalStatus(attemptStatus) || attemptStatus != status || finishedEventID != reference.EventID {
		return ErrClaimStale
	}
	return tx.validateRecordedRunnerReceipt(ctx, reference.StreamUID, reference.Attempt, reference.EventID, status)
}

func (tx *ImmediateTransaction) validateRecordedRunnerReceipt(ctx context.Context, streamUID string, attempt, eventID int64, status string) error {
	var receiptStatus, eventType string
	var eventAttempt int64
	err := tx.conn.QueryRowContext(ctx, `SELECT receipt.status,event.event_type,event.runner_attempt
		FROM transcript_runner_receipts receipt
		JOIN transcript_events event ON event.stream_uid=receipt.stream_uid AND event.event_id=receipt.event_id
		WHERE receipt.stream_uid=? AND receipt.attempt=? AND receipt.event_id=?`,
		streamUID, attempt, eventID).
		Scan(&receiptStatus, &eventType, &eventAttempt)
	if err != nil {
		return err
	}
	if eventType != "runner_finished" || eventAttempt != attempt ||
		!terminalStatus(receiptStatus) || receiptStatus != status {
		return ErrEventConflict
	}
	return nil
}
