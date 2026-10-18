package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Both POST and DELETE use this pure normalization before upload materialization
// or mutable capability validation. Cancellation never replays those effects.
func normalizeWebFrozenMessageInput(input *webConversationSendInput) error {
	if input.ReadingContext != nil && (input.ReadingContext.Validate() != nil || input.TurnIntent != "" || strings.TrimSpace(input.LoadingID) == "") {
		return &webConversationRequestError{Status: http.StatusBadRequest, Detail: "PDF reading selection is invalid for this message"}
	}
	input.Content, input.LoadingID = strings.TrimSpace(input.Content), strings.TrimSpace(input.LoadingID)
	if input.Content == "" || len(input.LoadingID) > 512 {
		return &webConversationRequestError{Status: http.StatusBadRequest, Detail: "message content and a valid loading_id are required"}
	}
	var err error
	input.InjectSkills, err = webAssistantStringList(input.InjectSkills, 64, 256)
	if err != nil {
		return &webConversationRequestError{Status: http.StatusBadRequest, Detail: "Invalid Skill selection"}
	}
	input.InjectMCPServerIDs, err = webAssistantStringList(input.InjectMCPServerIDs, 64, 256)
	if err != nil {
		return &webConversationRequestError{Status: http.StatusBadRequest, Detail: "Invalid MCP selection"}
	}
	if len(input.Files) == 0 {
		input.Files = nil
	}
	if len(input.ArtifactRefs) == 0 {
		input.ArtifactRefs = nil
	}
	if len(input.InjectSkills) == 0 {
		input.InjectSkills = nil
	}
	if len(input.InjectMCPServerIDs) == 0 {
		input.InjectMCPServerIDs = nil
	}
	if len(input.SessionOptions) == 0 {
		input.SessionOptions = nil
	}
	return nil
}

func webFrozenMessageDigest(input webConversationSendInput) (string, error) {
	if err := normalizeWebFrozenMessageInput(&input); err != nil {
		return "", err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Server) webFrozenMessageIntent(r *http.Request, frame workspace.CompatibilityFrame, input webConversationSendInput) (*transcriptstore.FrameMessageIntent, error) {
	if input.LoadingID == "" || s.transcriptStore == nil {
		return nil, nil
	}
	current, found, err := s.workspaceStore.GetFrameRealtimeContextWithContext(r.Context(), frame.ID)
	if err != nil {
		return nil, err
	}
	ownerID := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if !found || current.UserID != ownerID {
		return nil, transcriptstore.ErrOwnerMismatch
	}
	digest, err := webFrozenMessageDigest(input)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(webString(input.SessionOptions["target_branch_id"]))
	intent := transcriptstore.FrameMessageIntent{Version: 1, OwnerID: ownerID, FrameID: frame.ID,
		FrameIncarnationID: current.Frame.IncarnationID, LoadingID: input.LoadingID, TargetBranchID: branch,
		InputMessageID: transcriptstore.FrameMessageIntentID(frame.ID, input.LoadingID, branch), RequestSHA256: digest}
	if !intent.Valid() {
		return nil, &webConversationRequestError{Status: http.StatusBadRequest, Detail: "Invalid frozen message identity"}
	}
	if r.Method == http.MethodPost {
		// Preserve replay of pre-upgrade queued requests through their original
		// byte-match contract. They remain explicitly unavailable to typed DELETE.
		record, found, err := s.workspaceStore.GetCompatibilityMessageIntent(intent.InputMessageID)
		if err != nil {
			return nil, err
		}
		if found && record.FrameID != frame.ID {
			return nil, transcriptstore.ErrMessageIntentConflict
		}
		if found && record.DeliveryPayload["messageIntent"] == nil && record.DeliveryPayload["message_intent_cancellation"] == nil {
			if record.State == "retracted" {
				return nil, transcriptstore.ErrMessageIntentUnavailable
			}
			return nil, nil
		}
	}
	return &intent, nil
}

func (s *Server) handleWebMessageIntentCancellation(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame, encodedID string) {
	if r.Method != http.MethodDelete {
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
		return
	}
	loadingID, err := url.PathUnescape(encodedID)
	var input webConversationSendInput
	if err != nil || loadingID == "" || decodeWebConversationJSON(w, r, &input) != nil {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid frozen message request"})
		return
	}
	if err := normalizeWebFrozenMessageInput(&input); err != nil {
		writeWebConversationError(w, err)
		return
	}
	if input.LoadingID != loadingID {
		writeWebConversationError(w, transcriptstore.ErrMessageIntentConflict)
		return
	}
	intent, err := s.webFrozenMessageIntent(r, frame, input)
	if err != nil {
		writeWebConversationError(w, err)
		return
	}
	if intent == nil {
		writeWebConversationError(w, transcriptstore.ErrMessageIntentUnavailable)
		return
	}
	result, err := s.cancelWebMessageIntent(r, frame, *intent)
	if err != nil {
		writeWebConversationError(w, err)
		return
	}
	for _, event := range result.Events {
		if err := s.publishWorkspaceEvent(event); err != nil {
			writeWebConversationError(w, err)
			return
		}
	}
	receipt := result.Receipt
	writeWorkspaceJSON(w, http.StatusOK, map[string]any{
		"frame_id": receipt.Intent.FrameID, "loading_id": receipt.Intent.LoadingID,
		"input_message_id": receipt.Intent.InputMessageID, "request_sha256": receipt.Intent.RequestSHA256,
		"accepted": receipt.Accepted, "cancelled": receipt.Cancelled,
		"cancellation_status": receipt.CancellationStatus, "replayed": result.Replayed,
	})
}

func (s *Server) cancelWebMessageIntent(r *http.Request, frame workspace.CompatibilityFrame, intent transcriptstore.FrameMessageIntent) (workspace.FrameMessageIntentCancellationResult, error) {
	if frame.ParentFrameID != "" || frame.RootFrameID != frame.ID {
		return workspace.FrameMessageIntentCancellationResult{}, transcriptstore.ErrMessageIntentUnavailable
	}
	s.compatRequestMu.Lock()
	defer s.compatRequestMu.Unlock()
	frames, err := s.workspaceStore.ListFramesForRoot(frame.ID)
	if err != nil {
		return workspace.FrameMessageIntentCancellationResult{}, err
	}
	ids := make([]string, 0, len(frames))
	for _, frame := range frames {
		ids = append(ids, frame.ID)
	}
	var result workspace.FrameMessageIntentCancellationResult
	err = s.commitScopedSessionRunCancellation(ids, func(_ map[string]*transcriptRunnerAuthority) ([]string, error) {
		var err error
		result, err = s.workspaceStore.CancelFrameMessageIntent(r.Context(), intent)
		return result.CancelledFrameIDs, err
	})
	return result, err
}

func writeWebMessageIntentError(w http.ResponseWriter, err error) bool {
	code, status := "", http.StatusConflict
	switch {
	case errors.Is(err, transcriptstore.ErrFrameMessageStateStale):
		code = "CONVERSATION_MESSAGE_STATE_STALE"
	case errors.Is(err, transcriptstore.ErrMessageIntentCancelled):
		code = "MESSAGE_INTENT_CANCELLED"
	case errors.Is(err, transcriptstore.ErrMessageIntentConflict):
		code = "MESSAGE_INTENT_CONFLICT"
	case errors.Is(err, transcriptstore.ErrMessageIntentUnavailable):
		code = "MESSAGE_INTENT_UNAVAILABLE"
	case errors.Is(err, workspace.ErrFrameCancellationInputStale):
		code = "CONVERSATION_CANCEL_INPUT_STALE"
	case errors.Is(err, workspace.ErrFrameCancellationInputUnavailable):
		code = "CONVERSATION_CANCEL_INPUT_UNAVAILABLE"
	default:
		return false
	}
	w.Header().Set(webErrorCodeHeader, code)
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": err.Error()})
	return true
}

func (s *Server) createCompatibilityMessageResumeDispatch(frame workspace.CompatibilityFrame, reason string, intent *transcriptstore.FrameMessageIntent) (workspace.ResumeCompatibilityFrameResult, error) {
	if intent != nil {
		return s.workspaceStore.CreateMessageAutoResumeDispatch(frame.ID, frame.RootFrameID, frame.ProjectID, frame.AgentName, reason, *intent)
	}
	return s.workspaceStore.CreateAutoResumeDispatch(frame.ID, frame.RootFrameID, frame.ProjectID, frame.AgentName, reason)
}
