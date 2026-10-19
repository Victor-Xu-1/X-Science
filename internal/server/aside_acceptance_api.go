package server

import (
	"errors"
	"net/http"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

func compatibilityAsideAcceptanceResponse(result workspace.CompatibilityAsideResult) map[string]any {
	if cancelled := result.Cancellation; cancelled != nil {
		response := map[string]any{"intent_id": cancelled.IntentID, "accepted": false, "cancelled": true, "cancellation_status": "cancelled_before_acceptance", "replayed": result.Replayed}
		if cancelled.SourceSpecialist != nil {
			response["source_specialist"] = cancelled.SourceSpecialist
		}
		return response
	}
	if result.Acceptance == nil {
		// Legacy creation has no canonical acceptance envelope. Preserve its
		// original response instead of claiming a durable typed receipt.
		return map[string]any{"frame_id": result.Frame.ID, "prefix_len": result.PrefixLen}
	}
	response := map[string]any{"accepted": true, "cancelled": false, "frame_id": result.Frame.ID, "prefix_len": result.PrefixLen, "replayed": result.Replayed}
	if accepted := result.Acceptance; accepted != nil {
		response["intent_id"] = accepted.IntentID
		response["input_message_id"] = accepted.InputMessageID
		response["source_branch_id"] = accepted.SourceBranchID
		response["source_generation"] = accepted.SourceGeneration
		response["through_publication_sequence"] = accepted.ThroughPublication
		response["as_session"] = accepted.AsSession
		if accepted.SourceSpecialist != nil {
			response["source_specialist"] = accepted.SourceSpecialist
			response["specialist_binding"] = accepted.SpecialistBinding
		}
	}
	if result.CancelledInputMessageID != "" {
		response["cancelled"] = true
		response["cancellation_status"] = "cancelled"
		response["cancelled_input_message_id"] = result.CancelledInputMessageID
	}
	return response
}

// The router has already authenticated and authorized the parent. Storage
// repeats owner/parent checks and only a successful absent read is accepted:false.
func (s *Server) handleCompatibilityAsideAcceptance(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame) {
	intentID := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("intent_id")))
	if !compatibilityIntentUUIDPattern.MatchString(intentID) {
		writeV11Detail(w, http.StatusBadRequest, "intent_id must be a UUID")
		return
	}
	result, found, err := s.workspaceStore.GetCompatibilityAsideAcceptance(r.Context(), compatAgentUserID(r), frame.ID, intentID)
	if err != nil {
		writeCompatibilityAsideError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"accepted": false, "intent_id": intentID})
		return
	}
	writeJSON(w, http.StatusOK, compatibilityAsideAcceptanceResponse(result))
}

func writeCompatibilityAsideError(w http.ResponseWriter, err error) {
	if writeConversationAvailabilityError(w, err) {
		return
	}
	status, code, detail := 0, "", ""
	switch {
	case errors.Is(err, sessionagent.ErrInvalid):
		status, code, detail = http.StatusBadRequest, sessionagent.ErrInvalid.Error(), "Invalid frozen specialist source"
	case errors.Is(err, sessionagent.ErrRevisionConflict):
		status, code, detail = http.StatusConflict, sessionagent.ErrRevisionConflict.Error(), "Specialist source changed before aside admission"
	case errors.Is(err, sessionagent.ErrScopeStale):
		status, code, detail = http.StatusConflict, sessionagent.ErrScopeStale.Error(), "Specialist source lifetime changed"
	case errors.Is(err, sessionagent.ErrTargetUnavailable):
		status, code, detail = http.StatusConflict, sessionagent.ErrTargetUnavailable.Error(), "Frozen specialist source is unavailable"
	case errors.Is(err, workspace.ErrCompatibilityAsideSpecialistModel):
		status, code, detail = http.StatusForbidden, "ASIDE_SPECIALIST_MODEL_FORBIDDEN", "Model exceeds the frozen specialist maximum"
	case errors.Is(err, sessionagent.ErrUnavailable):
		status, code, detail = http.StatusServiceUnavailable, sessionagent.ErrUnavailable.Error(), "Frozen specialist authority is unavailable"
	case errors.Is(err, workspace.ErrFrameCancellationInputStale):
		status, code, detail = http.StatusConflict, "CONVERSATION_CANCEL_INPUT_STALE", "Cancellation target is not the current owned input"
	case errors.Is(err, workspace.ErrFrameCancellationInputUnavailable):
		status, code, detail = http.StatusConflict, "CONVERSATION_CANCEL_INPUT_UNAVAILABLE", "Canonical cancellation target is unavailable"
	case errors.Is(err, transcriptstore.ErrOwnerMismatch):
		status, code, detail = http.StatusNotFound, "ASIDE_NOT_FOUND", "Aside acceptance not found"
	case errors.Is(err, workspace.ErrCompatibilityIntentPayloadMismatch):
		status, code, detail = http.StatusConflict, "ASIDE_INTENT_CONFLICT", "Aside intent identifies a different request"
	case errors.Is(err, workspace.ErrCompatibilityAsideAcceptanceUnavailable):
		status, code, detail = http.StatusConflict, "ASIDE_ACCEPTANCE_UNAVAILABLE", "Stored aside acceptance cannot be safely reconciled"
	case errors.Is(err, transcriptstore.ErrBranchStateStale):
		status, code, detail = http.StatusConflict, "CONVERSATION_BRANCH_STATE_STALE", "Conversation source branch changed"
	case errors.Is(err, transcriptstore.ErrBranchTargetNotFound), errors.Is(err, transcriptstore.ErrBranchRequestInvalid):
		status, code, detail = http.StatusBadRequest, "ASIDE_SOURCE_INVALID", "Source branch and positive expected generation are required together"
	case errors.Is(err, workspace.ErrCompatibilityAsideParentTranscriptMissing):
		status, code, detail = http.StatusConflict, "ASIDE_SOURCE_UNAVAILABLE", "Canonical aside source is unavailable"
	}
	if status != 0 {
		writeJSON(w, status, map[string]any{"code": code, "detail": detail})
		return
	}
	var requestErr *compatibilityAsideError
	if errors.As(err, &requestErr) {
		writeV11Detail(w, requestErr.Status, requestErr.Detail)
		return
	}
	writeV11StoreError(w, err)
}
