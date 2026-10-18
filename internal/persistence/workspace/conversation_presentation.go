package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	transcriptstore "x-science/internal/persistence/transcript"
)

// UpdateConversationPresentation also reports whether presentation changed.
// Callers publish only after this transaction commits, never for a no-op retry.
func (s *Store) UpdateConversationPresentation(ctx context.Context, input ConversationPresentationUpdate) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("workspace store is closed")
	}
	changed := false
	input.changed = &changed
	if input.Extra != nil {
		if err := ValidateConversationPresentationPatch(input.Extra); err != nil {
			return false, err
		}
	} else if input.Name == nil && input.Description == nil {
		return false, ErrConversationPresentationInvalid
	}
	_, pin := input.Extra["pinned"]
	_, sort := input.Extra["sortOrder"]
	if (pin || sort) && (input.ExpectedRevision == nil || input.IncarnationID == "") {
		return false, ErrConversationPresentationStale
	}
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := s.updateConversationPresentationTx(ctx, tx, input); err != nil {
			return err
		}
		if input.Name != nil || input.Description != nil {
			return updateCompatibilityFrameMetadataTx(ctx, tx, input.FrameID, UpdateCompatibilityFrameInput{Name: input.Name, TaskSummary: input.Description}, s.now().UTC())
		}
		return nil
	})
	return changed && err == nil, err
}

func (s *Store) updateConversationPresentationTx(ctx context.Context, tx workspaceTransaction, input ConversationPresentationUpdate) error {
	var owner, incarnation, raw string
	var parent sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.incarnation_id,frame.parent_frame_id,COALESCE(metadata.context_data,'{}')
		FROM frames frame JOIN projects project ON project.id=frame.project_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id WHERE frame.id=?`, input.FrameID).Scan(&owner, &incarnation, &parent, &raw)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != input.OwnerID {
		return transcriptstore.ErrOwnerMismatch
	}
	if err != nil {
		return err
	}
	if incarnation != input.IncarnationID || parent.Valid && parent.String != "" {
		return ErrConversationPresentationStale
	}
	if input.Extra == nil {
		return nil
	}
	var contextData map[string]any
	if json.Unmarshal([]byte(raw), &contextData) != nil || contextData == nil {
		return ErrConversationPresentationInvalid
	}
	value, present := contextData["web_extra"]
	extra, object := value.(map[string]any)
	if present && value != nil && !object {
		return ErrConversationPresentationInvalid
	}
	if extra == nil {
		extra = map[string]any{}
	}
	revision, err := presentationRevision(extra)
	if err != nil {
		return err
	}
	wasPinned := extra["pinned"] == true
	if _, sort := input.Extra["sortOrder"]; sort && extra["pinned"] != true {
		return ErrConversationPresentationStale
	}
	changed := false
	for key, value := range input.Extra {
		if key == "sortOrder" {
			value, _ = presentationNumber(value)
		}
		current := extra[key]
		if key == "pinned" && current == nil {
			current = false
		}
		if !reflect.DeepEqual(current, value) {
			changed = true
		}
	}
	if input.ExpectedRevision != nil && (*input.ExpectedRevision < 0 || *input.ExpectedRevision > revision || changed && *input.ExpectedRevision != revision) {
		return ErrConversationPresentationStale
	}
	if !changed {
		return nil
	}
	if revision >= 9007199254740991 {
		return ErrConversationPresentationInvalid
	}
	for key, value := range input.Extra {
		if key == "sortOrder" {
			value, _ = presentationNumber(value)
		}
		extra[key] = value
	}
	if pin, ok := input.Extra["pinned"].(bool); ok {
		if pin {
			if !wasPinned {
				extra["pinned_at"] = s.now().UTC().UnixMilli()
			}
		} else {
			delete(extra, "pinned_at")
			delete(extra, "sortOrder")
		}
	}
	if _, sort := input.Extra["sortOrder"]; sort && extra["pinned"] != true {
		return ErrConversationPresentationStale
	}
	extra[presentationRevisionKey] = revision + 1
	encoded, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES(?,json_object('web_extra',json(?)))
		ON CONFLICT(frame_id) DO UPDATE SET context_data=json_set(COALESCE(frame_runtime_metadata.context_data,'{}'),'$.web_extra',json(?))`, input.FrameID, string(encoded), string(encoded))
	if err == nil && input.changed != nil {
		*input.changed = true
	}
	return err
}

func (s *Store) ReorderPinnedConversations(ctx context.Context, ownerID, anchorID string, items []ConversationPinnedOrderItem) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("workspace store is closed")
	}
	if len(items) == 0 || len(items) > 1000 {
		return nil, ErrConversationPresentationInvalid
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.ID == "" || seen[item.ID] || item.IncarnationID == "" || item.ExpectedRevision < 0 {
			return nil, ErrConversationPresentationInvalid
		}
		seen[item.ID] = true
		if _, ok := presentationNumber(item.SortOrder); !ok {
			return nil, ErrConversationPresentationInvalid
		}
	}
	if !seen[anchorID] {
		return nil, ErrConversationPresentationInvalid
	}
	changedIDs := []string{}
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		for _, item := range items {
			revision := item.ExpectedRevision
			changed := false
			if err := s.updateConversationPresentationTx(ctx, tx, ConversationPresentationUpdate{OwnerID: ownerID, FrameID: item.ID, IncarnationID: item.IncarnationID, ExpectedRevision: &revision, changed: &changed, Extra: map[string]any{"sortOrder": item.SortOrder}}); err != nil {
				return err
			}
			if changed {
				changedIDs = append(changedIDs, item.ID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changedIDs, nil
}
