package server

import (
	"net/http"
	"strings"
)

// Global host configuration is not an account-owned workspace capability.
// Reuse the configured local operator identity and real web authentication;
// a client identity header must never confer operator privileges.
func (s *Server) requireHostOperator(w http.ResponseWriter, r *http.Request) bool {
	if s.allowWebFSLocalRead(r) {
		return true
	}
	if s.xscienceLinkAuth != nil {
		if user, authenticated := s.webUser(r); authenticated && user.ID == s.xscienceLinkAuth.user().ID {
			return true
		}
	}
	writeWorkspaceJSON(w, http.StatusForbidden, map[string]any{
		"ok": false, "code": "HOST_OPERATOR_REQUIRED",
		"error": "host configuration requires the configured operator account",
	})
	return false
}

// An operator may approve access for an existing account; ordinary accounts
// cannot elevate themselves by supplying a different grant recipient.
func (s *Server) hostGrantRecipient(w http.ResponseWriter, owner, requested string) (string, bool) {
	target := strings.TrimSpace(requested)
	if target == "" || target == owner {
		return owner, true
	}
	if s.webAccounts != nil {
		if _, found := s.webAccounts.UserByID(target); found {
			return target, true
		}
	}
	writeWorkspaceJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "grant recipient account not found"})
	return "", false
}
