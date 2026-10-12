package server

import "net/http"

// Global host configuration is not an account-owned workspace capability.
// Reuse the configured local operator identity and real web authentication;
// a client identity header must never confer operator privileges.
func (s *Server) requireHostOperator(w http.ResponseWriter, r *http.Request) bool {
	if s.allowWebFSLocalRead(r) {
		return true
	}
	if s.xscienceLinkAuth != nil && s.xscienceLinkAuth.enabled {
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
