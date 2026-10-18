package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

var ErrCompatibilityAsideAcceptanceUnavailable = errors.New("aside acceptance binding is unavailable")

// Stored inside the existing durable intent envelope, in the same transaction
// as its child, selected history and first input. It is not a second queue.
type CompatibilityAsideAcceptance struct {
	OwnerID             string `json:"owner_id"`
	ParentRootFrameID   string `json:"parent_frame_id"`
	ParentIncarnationID string `json:"parent_incarnation_id"`
	IntentID            string `json:"intent_id"`
	FrameID             string `json:"frame_id"`
	InputMessageID      string `json:"input_message_id"`
	SourceBranchID      string `json:"source_branch_id"`
	SourceGeneration    int64  `json:"source_generation"`
	ThroughPublication  int64  `json:"through_publication_sequence"`
	PrefixLen           int    `json:"prefix_len"`
	AsSession           bool   `json:"as_session"`
}

func compatibilityAsideIntentPayload(input CompatibilityAsideInput, ownerID string) map[string]any {
	return map[string]any{
		"_aside_contract": 1, "owner_id": ownerID, "parent_frame_id": input.ParentRootFrameID,
		"request": input.Request, "model": input.Model, "as_session": input.AsSession,
		"source_branch_id": input.SourceBranchID, "expected_generation": input.ExpectedGeneration,
	}
}

func (s *Store) GetCompatibilityAsideAcceptance(ctx context.Context, ownerID, parentID, intentID string) (CompatibilityAsideResult, bool, error) {
	return s.readCompatibilityAsideAcceptance(ctx, ownerID, parentID, intentID, nil)
}

func (s *Store) ResolveCompatibilityAsideIntent(ctx context.Context, ownerID string, input CompatibilityAsideInput) (CompatibilityAsideResult, bool, error) {
	return s.readCompatibilityAsideAcceptance(ctx, ownerID, input.ParentRootFrameID, input.IntentID, &input)
}

func (s *Store) readCompatibilityAsideAcceptance(ctx context.Context, ownerID, parentID, intentID string, wanted *CompatibilityAsideInput) (CompatibilityAsideResult, bool, error) {
	if s == nil || s.db == nil {
		return CompatibilityAsideResult{}, false, errors.New("workspace store is closed")
	}
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(parentID) == "" || strings.TrimSpace(intentID) == "" {
		return CompatibilityAsideResult{}, false, errors.New("complete aside acceptance identity is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	defer tx.Rollback()
	result, found, err := lookupCompatibilityAsideIntent(ctx, tx, ownerID, parentID, intentID, wanted)
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	return result, found, nil
}

func lookupCompatibilityAsideIntent(ctx context.Context, tx workspaceTransaction, ownerID, parentID, intentID string, wanted *CompatibilityAsideInput) (CompatibilityAsideResult, bool, error) {
	var parentOwner, parentIncarnation string
	err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.incarnation_id FROM frames frame JOIN projects project ON project.id=frame.project_id WHERE frame.id=?`, parentID).Scan(&parentOwner, &parentIncarnation)
	if errors.Is(err, sql.ErrNoRows) || err == nil && parentOwner != ownerID {
		return CompatibilityAsideResult{}, false, transcriptstore.ErrOwnerMismatch
	}
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	if intentID == "" {
		return CompatibilityAsideResult{}, false, nil
	}
	record, err := scanCompatibilityMessageIntent(tx.QueryRowContext(ctx, compatibilityIntentSelect+` WHERE intent_id=?`, intentID))
	if errors.Is(err, sql.ErrNoRows) {
		return CompatibilityAsideResult{}, false, nil
	}
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	childOwner, err := frameOwnerInTransaction(ctx, tx, record.FrameID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && childOwner != ownerID {
		return CompatibilityAsideResult{}, false, transcriptstore.ErrOwnerMismatch
	}
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	if !isDurableAsideLifecycleRecord(record) {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	if record.State == "retracted" {
		return resolveCompatibilityAsideCancellation(record, ownerID, parentID, parentIncarnation, wanted)
	}
	raw, err := json.Marshal(record.DeliveryPayload["aside_acceptance"])
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	var accepted CompatibilityAsideAcceptance
	if json.Unmarshal(raw, &accepted) != nil || accepted.IntentID != intentID || accepted.FrameID != record.FrameID ||
		accepted.OwnerID != ownerID || accepted.ParentRootFrameID == "" || accepted.ParentIncarnationID == "" || accepted.InputMessageID == "" ||
		accepted.SourceBranchID == "" || accepted.SourceGeneration <= 0 || accepted.PrefixLen < 0 || record.State != "drained" {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	if accepted.ParentRootFrameID != parentID {
		if wanted == nil {
			return CompatibilityAsideResult{}, false, transcriptstore.ErrOwnerMismatch
		}
		return CompatibilityAsideResult{}, false, ErrCompatibilityIntentPayloadMismatch
	}
	if parentIncarnation != accepted.ParentIncarnationID {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	if err := matchCompatibilityAsideIntentPayload(record, wanted, ownerID); err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	frame, found, err := queryTraceFrame(ctx, tx, record.FrameID)
	if err != nil {
		return CompatibilityAsideResult{}, false, err
	}
	if !found || frame.RootFrameID != frame.ID || frame.ParentFrameID != "" {
		return CompatibilityAsideResult{}, false, ErrCompatibilityAsideAcceptanceUnavailable
	}
	result := CompatibilityAsideResult{Frame: frame, PrefixLen: accepted.PrefixLen, Acceptance: &accepted, Replayed: true}
	if frame.Status == "cancelled" {
		input, _, err := readFrameCancellationInputTx(ctx, tx, ownerID, frame.ID)
		if err != nil {
			return CompatibilityAsideResult{}, false, err
		}
		result.CancelledInputMessageID = input
	}
	return result, true, nil
}

func matchCompatibilityAsideIntentPayload(record CompatibilityMessageIntent, wanted *CompatibilityAsideInput, ownerID string) error {
	if wanted == nil {
		return nil
	}
	want, err := json.Marshal(compatibilityAsideIntentPayload(*wanted, ownerID))
	if err != nil {
		return err
	}
	got, err := json.Marshal(record.DedupePayload)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return ErrCompatibilityIntentPayloadMismatch
	}
	return nil
}
