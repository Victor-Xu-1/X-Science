package server

import (
	"encoding/json"
	"net/http"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleCompatibilityCancelAsideIntent(w http.ResponseWriter, r *http.Request, parent workspace.CompatibilityFrame) {
	if parent.ParentFrameID != "" {
		writeV11Detail(w, http.StatusBadRequest, "Aside parent must be a root frame")
		return
	}
	if s.transcriptStore == nil {
		writeCompatibilityAsideError(w, workspace.ErrCompatibilityAsideParentTranscriptMissing)
		return
	}
	var body struct {
		compatibilityAsideRequest
		ExpectedInputMessageID json.RawMessage `json:"expected_input_message_id"`
	}
	if err := decodeAgentCompatJSON(r, &body); err != nil {
		writeV11Detail(w, http.StatusBadRequest, "Frozen aside request is required")
		return
	}
	intent := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("intent_id")))
	body.SourceBranchID = strings.TrimSpace(body.SourceBranchID)
	if !compatibilityIntentUUIDPattern.MatchString(intent) || body.Request == "" ||
		body.IntentID != nil && strings.ToLower(strings.TrimSpace(*body.IntentID)) != intent ||
		(body.SourceBranchID == "") != (body.ExpectedGeneration == 0) || body.ExpectedGeneration < 0 ||
		body.SourceBranchID != "" && !validWebTranscriptBranchID(body.SourceBranchID) {
		writeV11Detail(w, http.StatusBadRequest, "Invalid frozen aside cancellation request")
		return
	}
	expected := ""
	if len(body.ExpectedInputMessageID) > 0 {
		if json.Unmarshal(body.ExpectedInputMessageID, &expected) != nil || strings.TrimSpace(expected) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "CONVERSATION_CANCEL_INPUT_INVALID", "detail": "expected_input_message_id must be a non-empty string"})
			return
		}
		expected = strings.TrimSpace(expected)
	}
	input := workspace.CompatibilityAsideInput{ParentRootFrameID: parent.ID, IntentID: intent, Request: body.Request, Model: body.Model, AsSession: body.AsSession, SourceBranchID: body.SourceBranchID, ExpectedGeneration: body.ExpectedGeneration}
	s.compatRequestMu.Lock()
	result, err := s.workspaceStore.CancelCompatibilityAsideIntent(r.Context(), compatAgentUserID(r), input)
	s.compatRequestMu.Unlock()
	if err != nil {
		writeCompatibilityAsideError(w, err)
		return
	}
	if result.Cancellation != nil {
		writeJSON(w, http.StatusOK, compatibilityAsideAcceptanceResponse(result))
		return
	}
	if result.Acceptance == nil {
		writeCompatibilityAsideError(w, workspace.ErrCompatibilityAsideAcceptanceUnavailable)
		return
	}
	if expected == "" {
		expected = result.Acceptance.InputMessageID
	}
	// Do not hold the request mutex recursively. This is the existing exact-input
	// cancellation pipeline; its final transaction rejects a newer follow-up.
	cancelled, err := s.cancelCompatibilityFrameTree(r.Context(), workspace.CompatibilityFrame{Frame: result.Frame}, "aside_closed", expected)
	if err != nil {
		writeCompatibilityAsideError(w, err)
		return
	}
	for _, event := range cancelled.Events {
		if err := s.publishWorkspaceEvent(event); err != nil {
			writeV11StoreError(w, err)
			return
		}
	}
	result.CancelledInputMessageID = ""
	for _, id := range cancelled.CancelledFrameIDs {
		if id == result.Frame.ID {
			result.CancelledInputMessageID = expected
			break
		}
	}
	response := compatibilityAsideAcceptanceResponse(result)
	response["target_input_message_id"] = expected
	if result.CancelledInputMessageID == "" {
		response["cancellation_status"] = "already_terminal"
	}
	writeJSON(w, http.StatusOK, response)
}
