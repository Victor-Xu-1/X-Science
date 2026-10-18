package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

type FrameMessageIntentCancellationResult struct {
	Receipt           transcriptstore.FrameMessageIntentCancellation
	Replayed          bool
	CancelledFrameIDs []string
	Events            []FrameEvent
}

// Preflight avoids materializing uploads for a known cancellation. The final
// append/queue transaction repeats the guard and remains the authority.
func (s *Store) CheckFrameMessageIntent(ctx context.Context, intent transcriptstore.FrameMessageIntent) error {
	return transcriptstore.CheckFrameMessageIntentAdmission(ctx, s.readDatabase(), intent.FrameID, intent.InputMessageID, &intent)
}

func (s *Store) CancelFrameMessageIntent(ctx context.Context, intent transcriptstore.FrameMessageIntent) (FrameMessageIntentCancellationResult, error) {
	var result FrameMessageIntentCancellationResult
	if s == nil || s.db == nil {
		return result, errors.New("workspace store is closed")
	}
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := transcriptstore.ValidateFrameMessageIntentScope(ctx, tx, intent); err != nil {
			return err
		}
		var parent sql.NullString
		var root, status string
		if err := tx.QueryRowContext(ctx, `SELECT parent_frame_id,root_frame_id,status FROM frames WHERE id=?`, intent.FrameID).Scan(&parent, &root, &status); err != nil {
			return err
		}
		// The existing tree cancellation owns an entire root. Never apply it to
		// a delegated child, which would implicitly cancel its parent's task.
		if parent.Valid && parent.String != "" || root != intent.FrameID {
			return transcriptstore.ErrMessageIntentUnavailable
		}
		if receipt, found, err := transcriptstore.ReadFrameMessageIntentCancellation(ctx, tx, intent); err != nil {
			return err
		} else if found {
			result.Receipt, result.Replayed = receipt, true
			return nil
		}
		accepted, err := transcriptstore.ReadAcceptedFrameMessageIntent(ctx, tx, intent)
		if err != nil {
			return err
		}
		record, err := scanCompatibilityMessageIntent(tx.QueryRowContext(ctx, compatibilityIntentSelect+` WHERE intent_id=?`, intent.InputMessageID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		found := err == nil
		if found && record.FrameID != intent.FrameID {
			return transcriptstore.ErrMessageIntentConflict
		}
		if found && record.State != "queued" && record.State != "delivering" && record.State != "drained" {
			return transcriptstore.ErrMessageIntentUnavailable
		}
		if !accepted && found && record.State == "drained" {
			return transcriptstore.ErrMessageIntentUnavailable
		}
		now := s.now().UTC()
		// Removing this pending delivery is atomic with cancellation. Other queued
		// inputs still make exact accepted-input cancellation fail closed.
		if found {
			if _, err := tx.ExecContext(ctx, `UPDATE queued_user_messages SET state='retracted',resolved_at=? WHERE intent_id=? AND frame_id=?`, now, intent.InputMessageID, intent.FrameID); err != nil {
				return err
			}
		}
		result.Receipt = transcriptstore.FrameMessageIntentCancellation{Intent: intent, Accepted: accepted, Cancelled: true, CancellationStatus: "cancelled_before_acceptance"}
		if accepted {
			if err := requireFrameCancellationInputTx(ctx, tx, intent.OwnerID, intent.FrameID, intent.InputMessageID); err != nil {
				return err
			}
			if status == "completed" || status == "failed" {
				result.Receipt.Cancelled, result.Receipt.CancellationStatus = false, "already_terminal"
			} else {
				cancelled, err := cancelCompatibilityFrameTreeWithTranscriptTx(ctx, tx, intent.FrameID, "Cancelled frozen message intent", intent.InputMessageID, now)
				if err != nil {
					return err
				}
				result.CancelledFrameIDs, result.Events = cancelled.CancelledFrameIDs, cancelled.Events
				result.Receipt.CancellationStatus = "cancelled"
			}
		}
		dedupe, delivery := record.DedupePayload, record.DeliveryPayload
		if !found {
			dedupe, delivery = map[string]any{"messageIntent": intent}, map[string]any{"messageIntent": intent}
		}
		delivery["message_intent_cancellation"] = result.Receipt
		raw, err := marshalCompatibilityQueueEnvelope(dedupe, delivery, "")
		if err != nil {
			return err
		}
		if found {
			_, err = tx.ExecContext(ctx, `UPDATE queued_user_messages SET payload=?,state='retracted',resolved_at=? WHERE intent_id=? AND frame_id=?`, raw, now, intent.InputMessageID, intent.FrameID)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO queued_user_messages(sequence,frame_id,payload,intent_id,state,resolved_at,created_at)
				SELECT COALESCE(MAX(sequence),0)+1,?,?,?,'retracted',?,? FROM queued_user_messages`, intent.FrameID, raw, intent.InputMessageID, now, now)
		}
		if err != nil {
			return err
		}
		if record.QueuedEventID != "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM frame_events WHERE id=? AND frame_id=? AND event_type='queued_message'`, record.QueuedEventID, intent.FrameID)
		}
		return err
	})
	if err != nil {
		return FrameMessageIntentCancellationResult{}, err
	}
	if len(result.CancelledFrameIDs) > 0 {
		s.signalKernelRetentionWake()
	}
	return result, nil
}

func isDurableMessageIntentCancellation(record CompatibilityMessageIntent) bool {
	if record.State != "retracted" {
		return false
	}
	raw, err := json.Marshal(record.DeliveryPayload["message_intent_cancellation"])
	var receipt transcriptstore.FrameMessageIntentCancellation
	return err == nil && json.Unmarshal(raw, &receipt) == nil && receipt.Valid() &&
		receipt.Intent.FrameID == record.FrameID && receipt.Intent.InputMessageID == record.IntentID
}
