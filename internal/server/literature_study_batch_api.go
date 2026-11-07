package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
	workspace "x-science/internal/persistence/workspace"
)

const literatureStudyBatchSettlement = 5 * time.Second

func writeStudyBatchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspace.ErrLiteratureStudyBatchBusy):
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "LITERATURE_STUDY_BATCH_BUSY"})
	case errors.Is(err, workspace.ErrLiteratureStudyBatchConflict), errors.Is(err, workspace.ErrLiteratureStudyBatchClaim):
		writeWorkspaceJSON(w, http.StatusConflict, map[string]any{"code": "LITERATURE_STUDY_BATCH_CHANGED"})
	default:
		writeLiteratureStudyError(w, err)
	}
}

func (s *Server) handleLiteratureStudyBatch(w http.ResponseWriter, r *http.Request, owner string, parts []string) {
	if r.Method == http.MethodGet && len(r.URL.Query()) == 0 {
		if len(parts) == 1 {
			batch, err := s.workspaceStore.ReadLiteratureStudyBatch(r.Context(), owner)
			if err != nil {
				writeStudyBatchError(w, err)
				return
			}
			writeWorkspaceJSON(w, http.StatusOK, workspace.NewLiteratureStudyBatchView(batch))
			return
		}
		if len(parts) == 4 && parts[2] == "rows" {
			index, err := strconv.Atoi(parts[3])
			if err != nil {
				writeLiteratureError(w, workspace.ErrLiteratureInvalid)
				return
			}
			result, err := s.workspaceStore.ReadLiteratureStudyBatchResult(r.Context(), owner, parts[1], index)
			if err != nil {
				writeStudyBatchError(w, err)
				return
			}
			writeWorkspaceJSON(w, http.StatusOK, result)
			return
		}
	}
	if r.Method != http.MethodPost || len(parts) != 1 || len(r.URL.Query()) != 0 {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	var command workspace.LiteratureStudyBatchCommand
	if decodeLiteratureMutationBody(w, r, &command) != nil {
		writeLiteratureError(w, workspace.ErrLiteratureInvalid)
		return
	}
	var batch *workspace.LiteratureStudyBatch
	var err error
	if command.Action == "advance" {
		ctx, cancel := context.WithTimeout(r.Context(), literatureStudyDeadline)
		defer cancel()
		batch, err = s.advanceLiteratureStudyBatch(ctx, owner, command,
			func(ctx context.Context, source workspace.LiteratureStudySource) (literatureStudyEvidence, error) {
				return s.literatureStudyPDF(ctx, source, owner)
			},
			func(ctx context.Context, prompt string) (workspace.LiteratureStudySections, string, error) {
				return s.inferLiteratureStudy(ctx, owner, prompt)
			})
	} else {
		batch, err = s.workspaceStore.ControlLiteratureStudyBatch(r.Context(), owner, command)
	}
	if err != nil {
		writeStudyBatchError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, workspace.NewLiteratureStudyBatchView(batch))
}
