package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

type CompatibilityAsideCancellation struct {
	OwnerID             string                       `json:"owner_id"`
	ParentRootFrameID   string                       `json:"parent_frame_id"`
	ParentIncarnationID string                       `json:"parent_incarnation_id"`
	IntentID            string                       `json:"intent_id"`
	BeforeAcceptance    bool                         `json:"before_acceptance"`
	SourceSpecialist    *sessionagent.InputReference `json:"source_specialist,omitempty"`
}

// An absent intent is terminally reserved on the existing parent, without
// making it a queued prompt or creating a child. Creation checks this same row
// under BEGIN IMMEDIATE, so exactly one admission/cancellation outcome wins.
func (s *Store) CancelCompatibilityAsideIntent(ctx context.Context, ownerID string, input CompatibilityAsideInput) (CompatibilityAsideResult, error) {
	if s == nil || s.db == nil {
		return CompatibilityAsideResult{}, errors.New("workspace store is closed")
	}
	input.ParentRootFrameID = strings.TrimSpace(input.ParentRootFrameID)
	input.IntentID = strings.TrimSpace(input.IntentID)
	input.SourceBranchID = strings.TrimSpace(input.SourceBranchID)
	if err := normalizeCompatibilityAsideSpecialist(&input); err != nil {
		return CompatibilityAsideResult{}, err
	}
	if ownerID == "" || input.ParentRootFrameID == "" || input.IntentID == "" || input.Request == "" ||
		(input.SourceBranchID == "") != (input.ExpectedGeneration == 0) || input.ExpectedGeneration < 0 || input.SourceBranchID != "" && !transcriptstore.IsValidBranchID(input.SourceBranchID) {
		return CompatibilityAsideResult{}, errors.New("complete frozen aside cancellation identity is required")
	}
	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	var result CompatibilityAsideResult
	repository := transcriptstore.NewRepository(s.db)
	err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if existing, found, err := lookupCompatibilityAsideIntent(ctx, tx, ownerID, input.ParentRootFrameID, input.IntentID, &input); err != nil {
			return err
		} else if found {
			result = existing
			return nil
		}
		var incarnation string
		if err := tx.QueryRowContext(ctx, `SELECT incarnation_id FROM frames WHERE id=?`, input.ParentRootFrameID).Scan(&incarnation); err != nil {
			return err
		}
		if incarnation == "" {
			return ErrCompatibilityAsideAcceptanceUnavailable
		}
		if input.SourceSpecialist != nil {
			scope, err := sessionagent.ResolveScope(ctx, tx, ownerID, input.ParentRootFrameID)
			if err != nil {
				return err
			}
			if scope != input.SourceSpecialist.Scope {
				return sessionagent.ErrScopeStale
			}
		}
		cancelled := &CompatibilityAsideCancellation{OwnerID: ownerID, ParentRootFrameID: input.ParentRootFrameID, ParentIncarnationID: incarnation, IntentID: input.IntentID, BeforeAcceptance: true, SourceSpecialist: input.SourceSpecialist}
		raw, err := marshalCompatibilityQueueEnvelope(compatibilityAsideIntentPayload(input, ownerID), map[string]any{"aside_cancellation": cancelled}, "")
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO queued_user_messages(sequence,frame_id,payload,intent_id,state,resolved_at,created_at)
			VALUES((SELECT COALESCE(MAX(sequence),0)+1 FROM queued_user_messages),?,?,?,'retracted',?,?)`, input.ParentRootFrameID, raw, input.IntentID, now, now); err != nil {
			return err
		}
		result = CompatibilityAsideResult{Cancellation: cancelled}
		return nil
	})
	return result, err
}

func resolveCompatibilityAsideCancellation(record CompatibilityMessageIntent, ownerID, parentID, parentIncarnation string, wanted *CompatibilityAsideInput) (CompatibilityAsideResult, bool, error) {
	raw, err := json.Marshal(record.DeliveryPayload["aside_cancellation"])
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	var cancelled CompatibilityAsideCancellation
	if json.Unmarshal(raw, &cancelled) != nil || !cancelled.BeforeAcceptance || record.FrameID != cancelled.ParentRootFrameID || cancelled.IntentID != record.IntentID || cancelled.OwnerID != ownerID {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	if cancelled.ParentRootFrameID != parentID {
		if wanted == nil {
			return CompatibilityAsideResult{}, false, transcriptstore.ErrOwnerMismatch
		}
		return CompatibilityAsideResult{}, false, ErrCompatibilityIntentPayloadMismatch
	}
	if cancelled.ParentIncarnationID != parentIncarnation {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	if err := matchCompatibilityAsideIntentPayload(record, wanted, ownerID); err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	if err := validateCompatibilityAsideSourceProof(record.DedupePayload, cancelled.SourceSpecialist, parentID, parentIncarnation); err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	return CompatibilityAsideResult{Cancellation: &cancelled, Replayed: true}, true, nil
}
