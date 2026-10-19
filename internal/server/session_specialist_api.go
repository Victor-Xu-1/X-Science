package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"x-science/internal/sessionagent"
)

func (s *Server) registerSessionSpecialistRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/frames/{frameId}/specialist", s.handleSessionSpecialist)
	mux.HandleFunc("POST /api/frames/{frameId}/specialist", s.handleSessionSpecialist)
}

func (s *Server) handleSessionSpecialist(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if owner == "" {
		writeWorkspaceJSON(w, http.StatusUnauthorized, map[string]any{"code": webAuthRequiredCode, "message": "Authentication required"})
		return
	}
	if s.workspaceStore == nil || s.transcriptStore == nil {
		writeSessionSpecialistError(w, sessionagent.ErrUnavailable)
		return
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(r.Context(), r.PathValue("frameId"))
	if err != nil {
		writeSessionSpecialistError(w, err)
		return
	}
	if !found || access.UserID != owner || access.IsHidden {
		writeSessionSpecialistError(w, sessionagent.ErrTargetUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		if len(r.URL.Query()) != 0 {
			writeSessionSpecialistError(w, sessionagent.ErrInvalid)
			return
		}
		result, err := s.workspaceStore.SessionSpecialist(r.Context(), owner, access.Frame.ID)
		if err != nil {
			writeSessionSpecialistError(w, err)
			return
		}
		result, err = s.legacySessionSpecialistReadback(access, result)
		if err != nil {
			writeSessionSpecialistError(w, err)
			return
		}
		writeWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	var command sessionagent.Command
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil || decoder.Decode(new(any)) != io.EOF || sessionagent.ValidateCommand(command) != nil || command.Scope.FrameID != access.Frame.ID {
		writeSessionSpecialistError(w, sessionagent.ErrInvalid)
		return
	}
	if result, replayed, err := s.workspaceStore.ReplaySessionSpecialist(r.Context(), owner, access.Frame.ID, command, false); err != nil {
		writeSessionSpecialistError(w, err)
		return
	} else if replayed {
		writeWorkspaceJSON(w, http.StatusOK, result)
		return
	}
	state, err := s.workspaceStore.SessionSpecialist(r.Context(), owner, access.Frame.ID)
	if err != nil {
		writeSessionSpecialistError(w, err)
		return
	}
	prepared, err := s.captureSessionSpecialistPreparation(r.Context(), owner, access.Frame.ID, command, state.Revision == 0)
	if err != nil {
		writeSessionSpecialistError(w, err)
		return
	}
	result, err := s.workspaceStore.PrepareSessionSpecialist(r.Context(), owner, access.Frame.ID, command, prepared)
	if err != nil {
		writeSessionSpecialistError(w, err)
		return
	}
	writeWorkspaceJSON(w, http.StatusOK, result)
}

func writeSessionSpecialistError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, sessionagent.ErrUnavailable.Error()
	switch {
	case errors.Is(err, sessionagent.ErrInvalid):
		status, code = http.StatusBadRequest, sessionagent.ErrInvalid.Error()
	case errors.Is(err, sessionagent.ErrTargetUnavailable):
		status, code = http.StatusNotFound, sessionagent.ErrTargetUnavailable.Error()
	case errors.Is(err, sessionagent.ErrScopeStale):
		status, code = http.StatusConflict, sessionagent.ErrScopeStale.Error()
	case errors.Is(err, sessionagent.ErrRevisionConflict):
		status, code = http.StatusConflict, sessionagent.ErrRevisionConflict.Error()
	case errors.Is(err, sessionagent.ErrCommandConflict):
		status, code = http.StatusConflict, sessionagent.ErrCommandConflict.Error()
	case errors.Is(err, sessionagent.ErrPending):
		status, code = http.StatusConflict, sessionagent.ErrPending.Error()
	case errors.Is(err, context.Canceled):
		status, code = http.StatusRequestTimeout, "SESSION_SPECIALIST_CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "SESSION_SPECIALIST_TIMEOUT"
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": code})
}
