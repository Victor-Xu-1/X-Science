package server

import (
	"encoding/json"
	"io"
	"net/http"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteratureBibtex(w http.ResponseWriter, r *http.Request, owner string) {
	var input struct {
		ItemIDs []string `json:"item_ids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var trailing any
	if len(r.URL.Query()) != 0 || decoder.Decode(&input) != nil || decoder.Decode(&trailing) != io.EOF {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	result, err := s.workspaceStore.ExportLiteratureBibtex(r.Context(), owner, input.ItemIDs)
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}
