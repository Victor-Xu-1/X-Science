package server

import (
	"errors"
	"net/http"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func manualNotebookRequireActor(w http.ResponseWriter, r *http.Request) bool {
	// withWebIdentity owns authentication and strips remote identity headers.
	// Do not use compatAgentUserID: its historical "local" fallback is not an
	// authenticated actor for the typed contract, nor is a body/query user ID.
	if strings.TrimSpace(r.Header.Get("X-Science-User-Id")) != "" {
		return true
	}
	writeManualNotebookAPIError(w, &manualNotebookAPIError{http.StatusUnauthorized, webAuthRequiredCode, "Authentication required"})
	return false
}

func (s *Server) manualNotebookAccess(r *http.Request, expected manualNotebookExpectedTarget) (workspace.KernelFrameAccess, error) {
	if s.workspaceStore == nil {
		return workspace.KernelFrameAccess{}, &manualNotebookAPIError{http.StatusServiceUnavailable, "MANUAL_EXECUTION_STORAGE_UNAVAILABLE", "Workspace storage is unavailable"}
	}
	frameID := r.PathValue("frameId")
	if !validManualNotebookID(frameID) {
		return workspace.KernelFrameAccess{}, manualNotebookInvalid("Invalid frame identity")
	}
	access, found, err := s.workspaceStore.GetKernelFrameAccess(frameID)
	if err != nil {
		return access, err
	}
	if !found || access.UserID != strings.TrimSpace(r.Header.Get("X-Science-User-Id")) || access.IsHidden {
		return access, transcriptstore.ErrOwnerMismatch
	}
	if access.Frame.IncarnationID != expected.FrameIncarnationID || access.Frame.RootFrameID != expected.RootFrameID ||
		access.RootFrameIncarnationID != expected.RootIncarnationID {
		return access, workspace.ErrManualKernelExecutionConflict
	}
	return access, nil
}

func (s *Server) loadManualNotebookReceipt(r *http.Request, access workspace.KernelFrameAccess, requestID string, expected manualNotebookExpectedTarget) (workspace.ManualKernelExecution, bool, error) {
	id := workspace.ManualKernelExecutionID(access.UserID, expected.FrameIncarnationID, requestID)
	record, found, err := s.workspaceStore.GetManualKernelExecution(r.Context(), access, id)
	if err != nil || !found {
		return record, found, err
	}
	if record.Request.RequestID != requestID || manualNotebookExpectedTargetFromRecord(record) != expected {
		return workspace.ManualKernelExecution{}, false, workspace.ErrManualKernelExecutionConflict
	}
	return record, true, nil
}

func writeManualNotebookAPIError(w http.ResponseWriter, err error) {
	status, code, detail := http.StatusServiceUnavailable, "MANUAL_EXECUTION_UNAVAILABLE", "Manual execution state is unavailable; retain the original request and query its receipt"
	var requestErr *manualNotebookAPIError
	switch {
	case errors.As(err, &requestErr):
		status, code, detail = requestErr.status, requestErr.code, requestErr.detail
	case errors.Is(err, transcriptstore.ErrOwnerMismatch):
		status, code, detail = http.StatusNotFound, "MANUAL_EXECUTION_FRAME_NOT_FOUND", "Frame not found"
	case errors.Is(err, errKernelHostAccessFenced):
		status, code, detail = http.StatusForbidden, "MANUAL_EXECUTION_ACCESS_FENCED", "Kernel access is fenced until isolation is re-established"
	case errors.Is(err, workspace.ErrManualKernelExecutionConflict):
		status, code, detail = http.StatusConflict, "MANUAL_EXECUTION_CONFLICT", "The request differs from the frozen scope, target or payload"
	case errors.Is(err, workspace.ErrManualKernelExecutionStale):
		status, code, detail = http.StatusConflict, "MANUAL_EXECUTION_STATE_STALE", "Execution state changed; query the original receipt"
	case errors.Is(err, workspace.ErrManualKernelExecutionInvalid):
		status, code, detail = http.StatusBadRequest, "MANUAL_EXECUTION_INVALID_REQUEST", "Invalid manual execution request"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(webErrorCodeHeader, code)
	writeJSON(w, status, map[string]any{"code": code, "detail": detail})
}
