package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Cancellation is a retracted envelope in the existing queue. It contains no
// accepted copy of the transcript, and cannot be claimed for delivery.
func ReadFrameMessageIntentCancellation(ctx context.Context, db MessageIntentQuery, intent FrameMessageIntent) (FrameMessageIntentCancellation, bool, error) {
	for _, id := range messageIntentCandidateIDs(intent) {
		var frameID, state, raw string
		err := db.QueryRowContext(ctx, `SELECT frame_id,state,payload FROM queued_user_messages WHERE intent_id=?`, id).Scan(&frameID, &state, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return FrameMessageIntentCancellation{}, false, err
		}
		if frameID != intent.FrameID {
			return FrameMessageIntentCancellation{}, false, ErrMessageIntentConflict
		}
		var envelope struct {
			Version  int `json:"_xscienceGoQueueVersion"`
			Delivery struct {
				Intent       *FrameMessageIntent             `json:"messageIntent"`
				Cancellation *FrameMessageIntentCancellation `json:"message_intent_cancellation"`
			} `json:"delivery"`
		}
		if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Version != 1 {
			return FrameMessageIntentCancellation{}, false, ErrMessageIntentUnavailable
		}
		if receipt := envelope.Delivery.Cancellation; receipt != nil {
			if state != "retracted" || !receipt.Valid() {
				return FrameMessageIntentCancellation{}, false, ErrMessageIntentUnavailable
			}
			if receipt.Intent != intent || id != intent.InputMessageID {
				return FrameMessageIntentCancellation{}, false, ErrMessageIntentConflict
			}
			return *receipt, true, nil
		}
		if envelope.Delivery.Intent == nil {
			return FrameMessageIntentCancellation{}, false, ErrMessageIntentUnavailable
		}
		if *envelope.Delivery.Intent != intent || id != intent.InputMessageID {
			return FrameMessageIntentCancellation{}, false, ErrMessageIntentConflict
		}
		if state == "retracted" {
			return FrameMessageIntentCancellation{}, false, ErrMessageIntentCancelled
		}
	}
	return FrameMessageIntentCancellation{}, false, nil
}

// The same guard runs at normal append, branch append and queued delivery,
// while their final write transaction is held. An HTTP preflight is not proof.
func CheckFrameMessageIntentAdmission(ctx context.Context, db MessageIntentQuery, frameID, clientID string, intent *FrameMessageIntent) error {
	var queueExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='queued_user_messages')`).Scan(&queueExists); err != nil {
		return err
	}
	if !queueExists {
		if intent != nil {
			return ErrSchemaUnavailable
		}
		return nil // Standalone Transcript repositories have no workspace queue.
	}
	if intent == nil {
		var state string
		err := db.QueryRowContext(ctx, `SELECT state FROM queued_user_messages WHERE frame_id=? AND intent_id=?`, frameID, clientID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state == "retracted" {
			return ErrMessageIntentCancelled
		}
		return nil
	}
	if intent.FrameID != frameID || intent.InputMessageID != clientID {
		return ErrMessageIntentConflict
	}
	if err := ValidateFrameMessageIntentScope(ctx, db, *intent); err != nil {
		return err
	}
	if _, found, err := ReadFrameMessageIntentCancellation(ctx, db, *intent); err != nil {
		return err
	} else if found {
		return ErrMessageIntentCancelled
	}
	// Legacy accepted messages remain replayable through the old event-match
	// contract, but cannot acquire invented typed proof during a retry.
	_, err := ReadAcceptedFrameMessageIntent(ctx, db, *intent)
	if errors.Is(err, ErrMessageIntentUnavailable) {
		return nil
	}
	return err
}
