package server

import (
	"net/http"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

// The original owner-scoped Store remains the only catalogue read authority.
func (s *Server) handleLiteratureItemRead(w http.ResponseWriter, r *http.Request, owner, id string) {
	if len(r.URL.Query()) != 0 || id == "" || len(id) > 512 || strings.TrimSpace(id) != id {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	item, err := s.workspaceStore.GetLiteratureItem(r.Context(), owner, id)
	if err == nil && item.Deleted {
		err = workspace.ErrLiteratureUnavailable
	}
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, item)
}
