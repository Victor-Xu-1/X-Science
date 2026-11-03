package server

import (
	"context"
	"errors"
	"net/http"
	workspace "x-science/internal/persistence/workspace"
)

var errLiteratureStudySourceUnavailable = errors.New("literature study source unavailable")
var errLiteratureStudyInputTooLarge = errors.New("literature study input too large")

func writeLiteratureStudyError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "LITERATURE_STUDY_FAILED"
	switch {
	case errors.Is(err, ErrRuntimeDraining):
		writeWorkspaceJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "runtime_draining", "message": "runtime is restarting; retry when ready"})
		return
	case errors.Is(err, errLiteratureStudyBusy):
		status, code = http.StatusTooManyRequests, "LITERATURE_STUDY_BUSY"
	case errors.Is(err, workspace.ErrLiteratureStudySourceChanged):
		status, code = http.StatusConflict, "LITERATURE_STUDY_SOURCE_CHANGED"
	case errors.Is(err, workspace.ErrLiteratureStudyPublicationConflict):
		status, code = http.StatusConflict, "LITERATURE_STUDY_CONFLICT"
	case errors.Is(err, errLiteratureStudySourceUnavailable):
		status, code = http.StatusUnprocessableEntity, "LITERATURE_STUDY_SOURCE_UNAVAILABLE"
	case errors.Is(err, errLiteratureStudyInputTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "LITERATURE_STUDY_INPUT_TOO_LARGE"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusRequestTimeout, "LITERATURE_STUDY_CANCELLED"
	case errors.Is(err, workspace.ErrLiteratureInvalid), errors.Is(err, workspace.ErrLiteratureUnavailable), errors.Is(err, workspace.ErrMutationIdempotencyConflict):
		writeLiteratureError(w, err)
		return
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code})
}
func (s *Server) handleLiteratureStudy(w http.ResponseWriter, r *http.Request, owner, id string) {
	if r.Method == http.MethodGet {
		query := r.URL.Query()
		if len(query) != 1 || len(query["language"]) != 1 || !workspace.ValidLiteratureStudyLanguage(query.Get("language")) {
			writeLiteratureError(w, workspace.ErrLiteratureInvalid)
			return
		}
		_, result, err := s.workspaceStore.ReadLiteratureStudy(r.Context(), owner, id, query.Get("language"))
		if err != nil {
			writeLiteratureStudyError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "METHOD_NOT_ALLOWED"})
		return
	}
	var input workspace.LiteratureStudyGenerate
	if decodeLiteratureMutationBody(w, r, &input) != nil || !validLiteratureOperation(input.OperationID) {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), literatureStudyDeadline)
	defer cancel()
	result, err := s.generateLiteratureStudy(ctx, owner, id, input,
		func(ctx context.Context, source workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
			return s.literatureStudyPDF(ctx, source, owner)
		},
		func(ctx context.Context, prompt string) (workspace.LiteratureStudySections, string, error) {
			return s.inferLiteratureStudy(ctx, owner, prompt)
		})
	if err != nil {
		writeLiteratureStudyError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}
