package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type webConversationPresentationInput struct {
	Name                  *string                           `json:"name"`
	Description           *string                           `json:"desc"`
	Extra                 map[string]any                    `json:"extra"`
	MergeExtra            bool                              `json:"merge_extra"`
	ExpectedIncarnationID string                            `json:"expected_incarnation_id"`
	ExpectedRevision      *int64                            `json:"expected_presentation_revision"`
	PinnedOrder           []webConversationPinnedOrderInput `json:"pinned_order"`
}

type webConversationPinnedOrderInput struct {
	ID               string  `json:"id"`
	IncarnationID    string  `json:"expected_incarnation_id"`
	ExpectedRevision *int64  `json:"expected_presentation_revision"`
	SortOrder        float64 `json:"sortOrder"`
}

func webConversationPinnedOrder(items []webConversationPinnedOrderInput) ([]workspace.ConversationPinnedOrderItem, error) {
	result := make([]workspace.ConversationPinnedOrderItem, 0, len(items))
	for _, item := range items {
		if item.ExpectedRevision == nil {
			return nil, workspace.ErrConversationPresentationInvalid
		}
		result = append(result, workspace.ConversationPinnedOrderItem{ID: item.ID, IncarnationID: item.IncarnationID, ExpectedRevision: *item.ExpectedRevision, SortOrder: item.SortOrder})
	}
	return result, nil
}

func (s *Server) handleWebConversationPresentationUpdate(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame) {
	var input webConversationPresentationInput
	if err := decodeWebConversationJSON(w, r, &input); err != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid conversation update"})
		return
	}
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > maxWebConversationNameRunes {
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "conversation name must contain 1 to 255 characters"})
			return
		}
		input.Name = &trimmed
	}
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	var err error
	changedIDs := []string{}
	if input.PinnedOrder != nil {
		if input.Name != nil || input.Description != nil || input.Extra != nil || input.ExpectedRevision != nil || input.ExpectedIncarnationID != "" || input.MergeExtra {
			err = workspace.ErrConversationPresentationInvalid
		} else {
			var items []workspace.ConversationPinnedOrderItem
			items, err = webConversationPinnedOrder(input.PinnedOrder)
			if err == nil {
				changedIDs, err = s.workspaceStore.ReorderPinnedConversations(r.Context(), owner, frame.ID, items)
			}
		}
	} else {
		incarnation := input.ExpectedIncarnationID
		_, pin := input.Extra["pinned"]
		_, sort := input.Extra["sortOrder"]
		if !pin && !sort && incarnation == "" {
			incarnation = frame.IncarnationID
		}
		var changed bool
		changed, err = s.workspaceStore.UpdateConversationPresentation(r.Context(), workspace.ConversationPresentationUpdate{
			OwnerID: owner, FrameID: frame.ID, IncarnationID: incarnation, ExpectedRevision: input.ExpectedRevision,
			Extra: input.Extra, Name: input.Name, Description: input.Description,
		})
		if changed {
			changedIDs = append(changedIDs, frame.ID)
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, workspace.ErrConversationPresentationStale):
			writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "CONVERSATION_PRESENTATION_STALE", "message": err.Error()})
		case errors.Is(err, workspace.ErrConversationPresentationInvalid):
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "CONVERSATION_PRESENTATION_INVALID", "message": err.Error()})
		case errors.Is(err, transcriptstore.ErrOwnerMismatch):
			writeWorkspaceJSON(w, http.StatusNotFound, map[string]any{"message": "conversation not found"})
		default:
			writeWebConversationError(w, err)
		}
		return
	}
	if len(changedIDs) == 0 && (input.Name != nil || input.Description != nil) {
		if err := s.publishWebConversationListChange(owner, frame.ID, "updated"); err != nil {
			writeWebConversationError(w, err)
			return
		}
	}
	for _, id := range changedIDs {
		if err := s.publishWebConversationListChange(owner, id, "updated", true); err != nil {
			writeWorkspaceJSON(w, http.StatusInternalServerError, map[string]any{"message": "conversation updated but realtime delivery failed"})
			return
		}
	}
	writeWorkspaceJSON(w, http.StatusOK, true)
}
