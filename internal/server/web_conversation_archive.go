package server

import (
	"errors"
	"net/http"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

type webConversationArchiveInput struct {
	Archived              *bool  `json:"archived"`
	ExpectedIncarnationID string `json:"expected_incarnation_id"`
	ExpectedRevision      *int64 `json:"expected_presentation_revision"`
}

func (s *Server) handleWebConversationArchive(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
		return
	}
	var input webConversationArchiveInput
	if err := decodeWebConversationJSON(w, r, &input); err != nil || input.Archived == nil || input.ExpectedRevision == nil ||
		strings.TrimSpace(input.ExpectedIncarnationID) == "" || *input.ExpectedRevision < 0 || *input.ExpectedRevision > 9007199254740991 {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "CONVERSATION_PRESENTATION_INVALID", "message": "invalid conversation archive request"})
		return
	}
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	result, err := s.workspaceStore.UpdateConversationArchive(r.Context(), workspace.ConversationArchiveUpdate{
		OwnerID: owner, FrameID: frame.ID, IncarnationID: input.ExpectedIncarnationID,
		ExpectedRevision: *input.ExpectedRevision, Archived: *input.Archived,
	})
	if err != nil {
		switch {
		case errors.Is(err, workspace.ErrConversationPresentationStale):
			writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "CONVERSATION_PRESENTATION_STALE", "message": err.Error()})
		case errors.Is(err, workspace.ErrConversationArchiveBusy):
			writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "CONVERSATION_ARCHIVE_BUSY", "message": err.Error()})
		case errors.Is(err, workspace.ErrConversationPresentationInvalid), errors.Is(err, transcriptstore.ErrConversationArchiveInvalid):
			writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"code": "CONVERSATION_PRESENTATION_INVALID", "message": err.Error()})
		case errors.Is(err, transcriptstore.ErrOwnerMismatch):
			writeWorkspaceJSON(w, http.StatusNotFound, map[string]any{"message": "conversation not found"})
		default:
			writeWebConversationError(w, err)
		}
		return
	}
	conversation, err := webConversationPresentationSnapshot(result.Frame, result.ProjectName)
	if err != nil {
		writeWebConversationError(w, err)
		return
	}
	if result.Changed {
		if err := s.publishWebConversationListChange(owner, frame.ID, "updated", true); err != nil {
			writeWorkspaceJSON(w, http.StatusInternalServerError, map[string]any{"message": "conversation archive updated but realtime delivery failed"})
			return
		}
	}
	// Callers reconcile a lost receipt through the existing canonical GET; this
	// command never retries with a newer revision or starts execution on restore.
	writeWorkspaceJSON(w, http.StatusOK, map[string]any{"conversation": conversation, "changed": result.Changed})
}

func writeConversationAvailabilityError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, transcriptstore.ErrConversationArchived) {
		return false
	}
	w.Header().Set(webErrorCodeHeader, "CONVERSATION_ARCHIVED")
	writeWorkspaceJSON(w, http.StatusGone, map[string]any{"code": "CONVERSATION_ARCHIVED", "message": err.Error(), "detail": err.Error()})
	return true
}
