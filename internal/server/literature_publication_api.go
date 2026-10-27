package server

import (
	"errors"
	"net/http"
	"strconv"

	"x-science/internal/literaturediscovery"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) handleLiteraturePublication(w http.ResponseWriter, r *http.Request, owner, item string) {
	if r.Method == http.MethodGet {
		query := r.URL.Query()
		values := query["expected_metadata_revision"]
		if len(query) != 1 || len(values) != 1 {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		revision, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || revision < 1 || revision > 9007199254740991 || strconv.FormatInt(revision, 10) != values[0] {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		result, err := s.workspaceStore.GetPublicationCheck(r.Context(), owner, item, revision)
		if err != nil {
			writeLiteratureError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "LITERATURE_METHOD_NOT_ALLOWED"})
		return
	}
	var input struct {
		OperationID      string `json:"operation_id"`
		ExpectedRevision int64  `json:"expected_metadata_revision"`
	}
	if r.URL.RawQuery != "" || decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) || input.ExpectedRevision < 1 || input.ExpectedRevision > 9007199254740991 {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	if s.publicationChecker == nil {
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "LITERATURE_PUBLICATION_UNAVAILABLE"})
		return
	}
	receipt, err := s.publicationChecker.Run(r.Context(), literaturediscovery.PublicationScope{
		OwnerUserID: owner, ItemID: item, OperationID: input.OperationID, ExpectedMetadataRevision: input.ExpectedRevision,
	})
	if errors.Is(err, literaturediscovery.ErrPublicationPending) {
		w.Header().Set("Retry-After", "1")
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "LITERATURE_PUBLICATION_PENDING"})
		return
	}
	if err != nil {
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, receipt)
}
