package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) registerDelegatedStopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/frames/{frameId}/delegated-stops", s.handleDelegatedStopSnapshot)
	mux.HandleFunc("POST /api/frames/{frameId}/delegated-stops", s.handleDelegatedStopSubmit)
	mux.HandleFunc("GET /api/frames/{frameId}/delegated-stops/{requestId}", s.handleDelegatedStopLookup)
}

func (s *Server) delegatedStopActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if owner == "" {
		w.Header().Set(webErrorCodeHeader, webAuthRequiredCode)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": webAuthRequiredCode, "message": "Authentication required"})
		return "", false
	}
	if s.workspaceStore == nil {
		writeDelegatedStopError(w, errors.New("workspace unavailable"))
		return "", false
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccessContext(r.Context(), r.PathValue("frameId"))
	if err != nil {
		writeDelegatedStopError(w, err)
		return "", false
	}
	if !found || access.UserID != owner || access.IsHidden {
		writeDelegatedStopError(w, workspace.ErrDelegatedStopUnavailable)
		return "", false
	}
	return owner, true
}

func (s *Server) handleDelegatedStopSnapshot(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.delegatedStopActor(w, r)
	if !ok {
		return
	}
	for key, values := range r.URL.Query() {
		if key != "branch_id" || len(values) != 1 {
			writeDelegatedStopError(w, workspace.ErrDelegatedStopInvalid)
			return
		}
	}
	snapshot, err := s.workspaceStore.DelegatedStopSnapshot(r.Context(), owner, r.PathValue("frameId"), r.URL.Query().Get("branch_id"))
	if err != nil {
		writeDelegatedStopError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleDelegatedStopSubmit(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.delegatedStopActor(w, r)
	if !ok {
		return
	}
	var request workspace.DelegatedStopRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Scope.ParentFrameID != r.PathValue("frameId") || workspace.ValidateDelegatedStopRequest(request) != nil {
		writeDelegatedStopError(w, workspace.ErrDelegatedStopInvalid)
		return
	}
	receipt, err := s.workspaceStore.AdmitDelegatedStop(r.Context(), owner, request)
	if err != nil {
		writeDelegatedStopError(w, err)
		return
	}
	// Exact durable cancellation won. No whole-root, weak execution-ID or
	// current/latest fallback is used for a missing physical registration.
	if _, err := s.signalDelegatedStop(r.Context(), owner, &receipt); err != nil {
		writeDelegatedStopError(w, err)
		return
	}
	receipt, err = s.workspaceStore.RecordDelegatedStopObservation(r.Context(), owner, receipt)
	if err != nil {
		writeDelegatedStopError(w, err)
		return
	}
	s.observeDelegatedStop(r.Context(), owner, &receipt)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleDelegatedStopLookup(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.delegatedStopActor(w, r)
	if !ok {
		return
	}
	receipt, found, err := s.workspaceStore.ReadDelegatedStop(r.Context(), owner, r.PathValue("frameId"), r.PathValue("requestId"))
	if err != nil {
		writeDelegatedStopError(w, err)
		return
	}
	if !found {
		writeDelegatedStopError(w, workspace.ErrDelegatedStopUnavailable)
		return
	}
	s.observeDelegatedStop(r.Context(), owner, &receipt)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, receipt)
}

func writeDelegatedStopError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "DELEGATED_STOP_STORAGE_UNAVAILABLE"
	switch {
	case errors.Is(err, workspace.ErrDelegatedStopInvalid):
		status, code = http.StatusBadRequest, workspace.ErrDelegatedStopInvalid.Error()
	case errors.Is(err, workspace.ErrDelegatedStopUnavailable):
		status, code = http.StatusNotFound, workspace.ErrDelegatedStopUnavailable.Error()
	default:
		for _, conflict := range []error{workspace.ErrDelegatedStopScopeStale, workspace.ErrDelegatedStopBranchStale, workspace.ErrDelegatedStopTargetStale, workspace.ErrDelegatedStopRequestConflict, workspace.ErrDelegatedStopOriginUnavailable} {
			if errors.Is(err, conflict) {
				status, code = http.StatusConflict, conflict.Error()
				break
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(webErrorCodeHeader, code)
	writeJSON(w, status, map[string]any{"code": code, "message": code})
}
