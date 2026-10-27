package server

import (
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteratureReadingStatus(w http.ResponseWriter, r *http.Request, owner string) {
	var input workspace.LiteratureReadingStatusInput
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
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
