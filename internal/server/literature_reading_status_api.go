package server

import (
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteratureReadingStatus(w http.ResponseWriter, r *http.Request, owner string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "LITERATURE_METHOD_NOT_ALLOWED"})
		return
	}
	var input workspace.LiteratureReadingStatusInput
	if r.URL.RawQuery != "" || decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	receipt, err := s.workspaceStore.SetLiteratureReadingStatus(r.Context(), owner, input)
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, receipt)
}
