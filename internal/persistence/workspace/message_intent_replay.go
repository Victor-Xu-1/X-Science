package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

type FrameMessageIntentReplay struct {
	Intent      transcriptstore.FrameMessageIntent
	State       string
	Snapshot    transcriptstore.FrameMessageIntentSnapshot
	Queue       CompatibilityMessageIntent
	FrameStatus string
	Root        bool
}

func readFrameMessageIntentReplay(ctx context.Context, tx workspaceTransaction, intent transcriptstore.FrameMessageIntent) (FrameMessageIntentReplay, bool, error) {
	result := FrameMessageIntentReplay{Intent: intent}
	if err := transcriptstore.ValidateFrameMessageIntentScope(ctx, tx, intent); err != nil {
		return result, false, err
	}
	if _, found, err := transcriptstore.ReadFrameMessageIntentCancellation(ctx, tx, intent); err != nil {
		return result, false, err
	} else if found {
		return result, false, transcriptstore.ErrMessageIntentCancelled
	}
	var rootID string
	var parent sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,root_frame_id,parent_frame_id FROM frames WHERE id=?`, intent.FrameID).Scan(&result.FrameStatus, &rootID, &parent); err != nil {
		return result, false, err
	}
	result.Root = rootID == intent.FrameID && (!parent.Valid || parent.String == "")
	snapshot, found, err := transcriptstore.ReadAcceptedFrameMessageSnapshot(ctx, tx, intent)
	if err != nil && !(errors.Is(err, transcriptstore.ErrMessageIntentUnavailable) && (!found || snapshot.Legacy)) {
		return result, false, err
	}
	if found && err == nil {
		result.State, result.Snapshot = "accepted", snapshot
		return result, true, nil
	}
	queue, err := scanCompatibilityMessageIntent(tx.QueryRowContext(ctx, compatibilityIntentSelect+` WHERE intent_id=?`, intent.InputMessageID))
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if queue.FrameID != intent.FrameID {
		return result, false, transcriptstore.ErrMessageIntentConflict
	}
	proof, err := transcriptstore.DecodeFrameMessageIntent(queue.DeliveryPayload["messageIntent"])
	if err != nil {
		return result, false, err
	}
	if proof == nil {
		return result, false, nil // Legacy keeps full-payload equality, not minted proof.
	}
	if *proof != intent {
		return result, false, transcriptstore.ErrMessageIntentConflict
	}
	if queue.State == "retracted" {
		return result, false, transcriptstore.ErrMessageIntentCancelled
	}
	if queue.State != "queued" && queue.State != "delivering" || queue.QueuedEventID == "" {
		return result, false, transcriptstore.ErrMessageIntentUnavailable
	}
	result.State, result.Queue = "queued", queue
	return result, true, nil
}

func (s *Store) LookupFrameMessageIntentReplay(ctx context.Context, intent transcriptstore.FrameMessageIntent) (FrameMessageIntentReplay, bool, error) {
	if s == nil || s.db == nil {
		return FrameMessageIntentReplay{}, false, ErrWorkspaceStoreClosed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return FrameMessageIntentReplay{}, false, err
	}
	defer tx.Rollback()
	result, found, err := readFrameMessageIntentReplay(ctx, tx, intent)
	if err != nil {
		return result, false, err
	}
	if err := tx.Commit(); err != nil {
		return FrameMessageIntentReplay{}, false, err
	}
	return result, found, nil
}

// Repair only an exact input that was appended but never claimed. Historical
// completion/failure/cancellation is not permission to start another attempt.
func (s *Store) ResumeUnclaimedFrameMessageIntent(ctx context.Context, intent transcriptstore.FrameMessageIntent) (ResumeCompatibilityFrameResult, error) {
	var result ResumeCompatibilityFrameResult
	if s == nil || s.db == nil {
		return result, ErrWorkspaceStoreClosed
	}
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		replay, found, err := readFrameMessageIntentReplay(ctx, tx, intent)
		if err != nil || !found {
			return err
		}
		if replay.State != "accepted" || !replay.Root || (replay.FrameStatus != "failed" && replay.FrameStatus != "cancelled") {
			return nil
		}
		snapshot := replay.Snapshot
		var inputRevision, consumedRevision, generation int64
		var branchID string
		var claimed bool
		err = tx.QueryRowContext(ctx, `SELECT stream.input_revision,stream.consumed_input_revision,branch.active_branch_id,branch.generation,
			EXISTS(SELECT 1 FROM transcript_runner_attempts attempt WHERE attempt.stream_uid=stream.stream_uid AND attempt.claimed_input_revision>=?)
			FROM transcript_streams stream JOIN transcript_branch_state branch ON branch.stream_uid=stream.stream_uid
			WHERE stream.stream_uid=?`, snapshot.InputRevision, snapshot.StreamUID).Scan(&inputRevision, &consumedRevision, &branchID, &generation, &claimed)
		if err != nil {
			return err
		}
		if claimed || consumedRevision >= snapshot.InputRevision || inputRevision != snapshot.InputRevision || branchID != snapshot.BranchID || generation != snapshot.BranchGeneration {
			return nil
		}
		var payload struct {
			RuntimeConfig map[string]any `json:"runtimeConfig"`
		}
		if err := json.Unmarshal(snapshot.PayloadJSON, &payload); err != nil {
			return transcriptstore.ErrMessageIntentUnavailable
		}
		var model *string
		if value, ok := payload.RuntimeConfig["model"].(string); ok && value != "" {
			model = &value
		}
		result, err = resumeCompatibilityFrameConversationTx(ctx, tx, intent.FrameID,
			ResumeCompatibilityFrameInput{MessageIntent: &intent, Model: model}, s.now().UTC())
		return err
	})
	return result, err
}
