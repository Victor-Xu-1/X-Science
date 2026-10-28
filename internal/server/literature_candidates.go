package server

import (
	"net/http"
	"strings"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteratureCandidates(w http.ResponseWriter, r *http.Request, owner string, parts []string) bool {
	if len(parts) == 0 || parts[0] != "candidates" {
		return false
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		if len(r.URL.Query()) > 2 || len(r.URL.Query()["state"]) != 1 || len(r.URL.Query()["cursor"]) > 1 || len(r.URL.Query()) == 2 && len(r.URL.Query()["cursor"]) != 1 {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return true
		}
		page, err := s.workspaceStore.ListLiteratureCandidates(r.Context(), owner, r.URL.Query().Get("state"), r.URL.Query().Get("cursor"))
		if err != nil {
			writeLiteratureError(w, err)
			return true
		}
		writeWorkspaceJSON(w, http.StatusOK, page)
		return true
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		var input workspace.LiteratureCandidateStage
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return true
		}
		receipt, err := s.workspaceStore.StageLiteratureCandidate(r.Context(), owner, input)
		if err != nil {
			writeLiteratureError(w, err)
			return true
		}
		writeWorkspaceJSON(w, http.StatusOK, receipt)
		return true
	}
	if len(parts) == 3 && r.Method == http.MethodPost && (parts[2] == "accept" || parts[2] == "dismiss" || parts[2] == "restore") && strings.TrimSpace(parts[1]) == parts[1] {
		var input workspace.LiteratureCandidateAction
		if decodePDFReadingBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return true
		}
		receipt, err := s.workspaceStore.DecideLiteratureCandidate(r.Context(), owner, parts[1], parts[2], input)
		if err != nil {
			writeLiteratureError(w, err)
			return true
		}
		writeWorkspaceJSON(w, http.StatusOK, receipt)
		return true
	}
	writeLiteratureError(w, workspace.ErrLiteratureInvalid)
	return true
}
