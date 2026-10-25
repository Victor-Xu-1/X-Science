package server

import (
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func writeLiteratureCollectionResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, value)
}

// The existing authenticated Library route owns admission and error handling.
// Collections use the same mutation ledger without an alternative API owner.
func (s *Server) handleLiteratureCollectionRoute(w http.ResponseWriter, r *http.Request, owner string, parts []string) bool {
	if len(parts) == 3 && parts[0] == "items" && parts[2] == "collections" && r.Method == http.MethodGet {
		if len(r.URL.Query()) != 0 {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return true
		}
		value, err := s.workspaceStore.GetLiteratureItemCollections(r.Context(), owner, parts[1])
		writeLiteratureCollectionResult(w, value, err)
		return true
	}
	if len(parts) == 0 || parts[0] != "collections" {
		return false
	}
	if len(r.URL.Query()) != 0 {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return true
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			value, err := s.workspaceStore.ListLiteratureCollections(r.Context(), owner)
			writeLiteratureCollectionResult(w, value, err)
			return true
		case http.MethodPost:
			var input workspace.LiteratureCollectionCreate
			if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return true
			}
			value, err := s.workspaceStore.CreateLiteratureCollection(r.Context(), owner, input)
			writeLiteratureCollectionResult(w, value, err)
			return true
		}
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodPatch:
			var input workspace.LiteratureCollectionUpdate
			if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return true
			}
			value, err := s.workspaceStore.UpdateLiteratureCollection(r.Context(), owner, parts[1], input)
			writeLiteratureCollectionResult(w, value, err)
			return true
		case http.MethodDelete:
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return true
			}
			value, err := s.workspaceStore.DeleteLiteratureCollection(r.Context(), owner, parts[1], input.OperationID)
			writeLiteratureCollectionResult(w, value, err)
			return true
		}
	}
	if len(parts) == 4 && parts[2] == "items" && r.Method == http.MethodPut {
		var input struct {
			OperationID string `json:"operation_id"`
			Included    *bool  `json:"included"`
		}
		if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) || input.Included == nil {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return true
		}
		value, err := s.workspaceStore.SetLiteratureCollectionItem(r.Context(), owner, parts[1], parts[3], input.OperationID, *input.Included)
		writeLiteratureCollectionResult(w, value, err)
		return true
	}
	return false
}
