package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// PublicStopReason is safe presentation metadata, never retry or execution authority.
type PublicStopReason string

const (
	PublicStopNoProgress          PublicStopReason = "no_progress"
	PublicStopSourceChanged       PublicStopReason = "source_changed"
	PublicStopCancelled           PublicStopReason = "cancelled"
	PublicStopModelProtocol       PublicStopReason = "model_protocol"
	PublicStopProviderUnavailable PublicStopReason = "provider_unavailable"
	PublicStopUnknown             PublicStopReason = "unknown"
)

func (reason PublicStopReason) Valid() bool {
	switch reason {
	case PublicStopNoProgress, PublicStopSourceChanged, PublicStopCancelled,
		PublicStopModelProtocol, PublicStopProviderUnavailable, PublicStopUnknown:
		return true
	default:
		return false
	}
}

type FrameRunnerTerminalOptions struct {
	PublicStopReason PublicStopReason
}

// FinishLatestFrameRunner settles an internally owned Frame runner without
// exposing a claim-token bypass on Repository. Callers must already hold the
// workspace transaction that owns the Frame lifecycle mutation.
func (tx *ImmediateTransaction) FinishLatestFrameRunner(
	ctx context.Context,
	ownerID, frameID, status, detail string,
	destinations []string,
	options ...FrameRunnerTerminalOptions,
) (Event, FinishReceipt, bool, error) {
	return tx.finishLatestFrameRunner(ctx, ownerID, frameID, status, detail, destinations, true, options...)
}

// projectFrame is false only after the recovery caller has verified an already
// failed Frame in this same transaction. It settles the orphaned attempt using
// the existing receipt authority without replacing the Frame's original cause.
func (tx *ImmediateTransaction) finishLatestFrameRunner(
	ctx context.Context,
	ownerID, frameID, status, detail string,
	destinations []string,
	projectFrame bool,
	options ...FrameRunnerTerminalOptions,
) (Event, FinishReceipt, bool, error) {
	if len(options) > 1 || len(options) == 1 && options[0].PublicStopReason != "" && !options[0].PublicStopReason.Valid() {
		return Event{}, FinishReceipt{}, false, errors.New("one optional allowlisted public stop reason is required")
	}
	ownerID = strings.TrimSpace(ownerID)
	frameID = strings.TrimSpace(frameID)
	status = normalizeTerminalStatus(status)
	detail = strings.TrimSpace(detail)
	if tx == nil || tx.repository == nil || tx.conn == nil || ownerID == "" || frameID == "" ||
		!terminalStatus(status) || detail == "" {
		return Event{}, FinishReceipt{}, false, errors.New("active transcript transaction, owner, frame id, terminal status, and detail are required")
	}
	stream, err := latestFrameStreamConn(ctx, tx.conn, ownerID, frameID)
	if err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	var attempt, claimedRevision int64
	var runnerID, currentStatus string
	err = tx.conn.QueryRowContext(ctx, `
		SELECT attempt,runner_id,claimed_input_revision,status
		FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, stream.UID,
	).Scan(&attempt, &runnerID, &claimedRevision, &currentStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, FinishReceipt{}, false, ErrClaimStale
	}
	if err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	terminal := map[string]string{"status": status, "detail": detail}
	if len(options) == 1 && options[0].PublicStopReason != "" {
		terminal["public_stop_reason"] = string(options[0].PublicStopReason)
	}
	payload, err := json.Marshal(terminal)
	if err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	destinations = terminalDeliveryDestinations(stream, destinations)
	clientMessageID := "runner-terminal:" + status + ":" + stream.UID + ":" + strconv.FormatInt(attempt, 10)
	if existing, found, err := findEventByClientID(ctx, tx.conn, stream.UID, clientMessageID); err != nil {
		return Event{}, FinishReceipt{}, false, err
	} else if found {
		record := eventRecord{
			clientMessageID: clientMessageID, eventType: "runner_finished", source: EventSourcePayload,
			runnerAttempt: &attempt, payloadJSON: payload, destinations: destinations, createdAt: existing.CreatedAt,
		}
		if !eventMatches(existing, record) {
			return Event{}, FinishReceipt{}, false, ErrEventConflict
		}
		storedDestinations, err := deliveryDestinations(ctx, tx.conn, stream.UID, existing.PublicationSeq)
		if err != nil {
			return Event{}, FinishReceipt{}, false, err
		}
		if !stringSlicesEqual(storedDestinations, normalizedDestinations(destinations)) {
			return Event{}, FinishReceipt{}, false, ErrEventConflict
		}
		receipt, err := loadRunnerReceiptConn(ctx, tx.conn, stream.UID, attempt)
		if err != nil || receipt.EventID != existing.EventID || receipt.Status != status {
			if err != nil {
				return Event{}, FinishReceipt{}, false, err
			}
			return Event{}, FinishReceipt{}, false, ErrEventConflict
		}
		if projectFrame {
			if err := applyFrameTerminalStatusConn(ctx, tx.conn, stream, status, detail, receipt.FinishedAt); err != nil {
				return Event{}, FinishReceipt{}, false, err
			}
		}
		return existing, receipt, false, nil
	}
	if currentStatus != "running" {
		return Event{}, FinishReceipt{}, false, ErrEventConflict
	}
	now := tx.repository.now().UTC()
	event, created, err := appendEventConn(ctx, tx.conn, stream, eventRecord{
		clientMessageID: clientMessageID, eventType: "runner_finished", source: EventSourcePayload,
		runnerAttempt: &attempt, payloadJSON: payload, destinations: destinations, createdAt: now,
	})
	if err != nil || !created {
		return Event{}, FinishReceipt{}, false, err
	}
	if _, err := bindCommittedArtifactReferencesConn(ctx, tx.conn, stream, attempt, event.EventID, now); err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	updated, err := tx.conn.ExecContext(ctx, `
		UPDATE transcript_runner_attempts SET status=?,phase='terminal',phase_sequence=phase_sequence+1,
			finished_event_id=?,finished_at=?
		WHERE stream_uid=? AND attempt=? AND runner_id=? AND status='running'`,
		status, event.EventID, now, stream.UID, attempt, runnerID)
	if err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	if rows, err := updated.RowsAffected(); err != nil || rows != 1 {
		return Event{}, FinishReceipt{}, false, ErrClaimStale
	}
	if _, err := tx.conn.ExecContext(ctx, `
		INSERT INTO transcript_runner_receipts(stream_uid,attempt,event_id,status,finished_at)
		VALUES(?,?,?,?,?)`, stream.UID, attempt, event.EventID, status, now); err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	if _, err := tx.conn.ExecContext(ctx, `
		UPDATE transcript_streams SET consumed_input_revision=MAX(consumed_input_revision,?) WHERE stream_uid=?`,
		claimedRevision, stream.UID); err != nil {
		return Event{}, FinishReceipt{}, false, err
	}
	if projectFrame {
		if err := applyFrameTerminalStatusConn(ctx, tx.conn, stream, status, detail, now); err != nil {
			return Event{}, FinishReceipt{}, false, err
		}
	}
	return event, FinishReceipt{
		StreamUID: stream.UID, Attempt: attempt, EventID: event.EventID, Status: status, FinishedAt: now,
	}, true, nil
}
