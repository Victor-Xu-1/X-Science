package server

import (
	"net/http"
	"strings"
)

// The former synchronous author/installer is retired. Old clients receive an
// explicit upgrade response, never a different model run or a second publishing
// authority. Access checks remain in place before revealing conversation state.
func (s *Server) handleSkillFromConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
		return
	}
	if s == nil || s.workspaceStore == nil {
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"message": "workspace storage is not configured"})
		return
	}
	var input struct {
		ConversationID string `json:"conversation_id"`
		Name           string `json:"name"`
	}
	if err := decodeAgentCompatJSON(r, &input); err != nil || strings.TrimSpace(input.ConversationID) == "" {
		writeWorkspaceJSON(w, http.StatusBadRequest, map[string]any{"message": "conversation_id is required"})
		return
	}
	if _, _, ok := s.webConversationAccess(w, r, input.ConversationID); !ok {
		return
	}
	writeWorkspaceJSON(w, http.StatusGone, map[string]any{
		"code":        "conversation_skill_action_required",
		"message":     "Save as Skill now runs in the active conversation. Reload the application and use its Save as Skill action.",
		"turn_intent": conversationSkillControlIntent,
	})
}
