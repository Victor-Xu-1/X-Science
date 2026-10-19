package server

import (
	"errors"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

// The workspace transaction resolves owner and the current root incarnation;
// neither runtime metadata nor a caller-provided owner can authorize this API.
func (s *Server) handleSessionDelegationPolicy(w http.ResponseWriter, r *http.Request, frameID string) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.Query()) != 0 {
		writeSessionDelegationError(w, workspace.ErrSessionDelegationInvalid)
		return
	}
	owner := compatAgentUserID(r)
	switch r.Method {
	case http.MethodGet:
		snapshot, err := s.workspaceStore.SessionDelegationPolicy(r.Context(), owner, frameID)
		if err != nil {
			writeSessionDelegationError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, snapshot)
	case http.MethodPost:
		var command workspace.SessionDelegationCommand
		if err := decodeWebConversationJSON(w, r, &command); err != nil {
			writeSessionDelegationError(w, workspace.ErrSessionDelegationInvalid)
			return
		}
		result, err := s.workspaceStore.ApplySessionDelegationCommand(r.Context(), owner, frameID, command)
		if err != nil {
			writeSessionDelegationError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, result)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"code": "METHOD_NOT_ALLOWED", "detail": "Use GET or POST for session delegation policy.",
		})
	}
}

func writeSessionDelegationError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "SESSION_DELEGATION_UNAVAILABLE"
	switch {
	case errors.Is(err, workspace.ErrSessionDelegationInvalid):
		status, code = http.StatusBadRequest, "SESSION_DELEGATION_INVALID"
	case errors.Is(err, workspace.ErrSessionDelegationTarget):
		status, code = http.StatusNotFound, "SESSION_DELEGATION_TARGET_UNAVAILABLE"
	case errors.Is(err, workspace.ErrSessionDelegationScopeStale):
		status, code = http.StatusConflict, "SESSION_DELEGATION_SCOPE_STALE"
	case errors.Is(err, workspace.ErrSessionDelegationConflict):
		status, code = http.StatusConflict, "SESSION_DELEGATION_COMMAND_CONFLICT"
	case errors.Is(err, workspace.ErrSessionDelegationRevision):
		status, code = http.StatusConflict, "SESSION_DELEGATION_REVISION_CONFLICT"
	}
	body := map[string]any{"code": code, "detail": "Session delegation policy could not be updated."}
	var revision *workspace.SessionDelegationRevisionError
	if errors.As(err, &revision) {
		body["snapshot"] = revision.Snapshot
	}
	writeWorkspaceJSON(w, status, body)
}
