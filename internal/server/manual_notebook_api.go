package server

import (
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) registerManualNotebookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/frames/{frameId}/kernel-executions", s.handleManualNotebookSubmit)
	mux.HandleFunc("GET /api/frames/{frameId}/kernel-executions", s.handleManualNotebookPending)
	mux.HandleFunc("GET /api/frames/{frameId}/kernel-executions/by-request/{requestId}", s.handleManualNotebookLookup)
	mux.HandleFunc("POST /api/frames/{frameId}/kernel-executions/by-request/{requestId}/cancel", s.handleManualNotebookCancel)
}

func (s *Server) handleManualNotebookSubmit(w http.ResponseWriter, r *http.Request) {
	if !manualNotebookRequireActor(w, r) {
		return
	}
	request, err := decodeManualNotebookRequest(w, r)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	access, err := s.manualNotebookAccess(r, request.manualNotebookExpectedTarget)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	// Recover accepted work before looking at current kernels or host grants.
	// Closing/restarting/revoking the original target cannot make a retry run
	// the source on a newer process or lose the original completed receipt.
	record, found, err := s.loadManualNotebookReceipt(r, access, request.RequestID, request.manualNotebookExpectedTarget)
	if err == nil && found && record.Request != request.storeRequest() {
		err = workspace.ErrManualKernelExecutionConflict
	}
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	if found {
		s.writeManualNotebookReceipt(w, http.StatusOK, record)
		return
	}
	if err := s.validateManualNotebookLiveTarget(access, request.manualNotebookExpectedTarget); err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	record, err = s.submitManualNotebookExecution(r.Context(), access, request.storeRequest())
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	s.writeManualNotebookReceipt(w, http.StatusAccepted, record)
}

func (s *Server) handleManualNotebookLookup(w http.ResponseWriter, r *http.Request) {
	if !manualNotebookRequireActor(w, r) {
		return
	}
	target, requestID, err := decodeManualNotebookLookup(r)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	access, err := s.manualNotebookAccess(r, target)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	record, found, err := s.loadManualNotebookReceipt(r, access, requestID, target)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	if !found {
		writeManualNotebookAPIError(w, &manualNotebookAPIError{http.StatusNotFound, "MANUAL_EXECUTION_NOT_ACCEPTED", "No accepted receipt is currently present; an in-flight submission may still arrive"})
		return
	}
	s.writeManualNotebookReceipt(w, http.StatusOK, record)
}

func (s *Server) handleManualNotebookCancel(w http.ResponseWriter, r *http.Request) {
	if !manualNotebookRequireActor(w, r) {
		return
	}
	request, err := decodeManualNotebookRequest(w, r)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	if request.RequestID != r.PathValue("requestId") {
		writeManualNotebookAPIError(w, workspace.ErrManualKernelExecutionConflict)
		return
	}
	access, err := s.manualNotebookAccess(r, request.manualNotebookExpectedTarget)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	record, found, err := s.loadManualNotebookReceipt(r, access, request.RequestID, request.manualNotebookExpectedTarget)
	if err == nil && found && record.Request != request.storeRequest() {
		err = workspace.ErrManualKernelExecutionConflict
	}
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	if !found {
		// V1 is accepted-only. No tombstone, fabricated cancellation or legacy
		// exact-ID fallback is allowed for an admission still in flight.
		writeManualNotebookAPIError(w, &manualNotebookAPIError{http.StatusConflict, "MANUAL_EXECUTION_NOT_ACCEPTED", "Cancellation requires the original accepted receipt"})
		return
	}
	if record.State == "terminal" {
		s.writeManualNotebookReceipt(w, http.StatusOK, record)
		return
	}
	if record.State == "outcome_unknown" {
		writeManualNotebookAPIError(w, &manualNotebookAPIError{http.StatusConflict, "MANUAL_EXECUTION_OUTCOME_UNKNOWN", "Execution outcome is unknown; no stopped state has been confirmed"})
		return
	}
	record, err = s.workspaceStore.RequestManualKernelExecutionCancellation(r.Context(), record)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	if record.State != "terminal" && record.ServerBootID == s.kernelOperationBootID && s.kernelManager != nil {
		// The Manager rechecks the original pointer plus complete identity at
		// its existing cancellation transition. Observation is not authority.
		s.kernelManager.InterruptExecution(manualNotebookExecutionIdentity(record))
	}
	current, found, err := s.loadManualNotebookReceipt(r, access, request.RequestID, request.manualNotebookExpectedTarget)
	if err == nil && (!found || current.ManualKernelBinding != record.ManualKernelBinding || current.Request != record.Request) {
		err = workspace.ErrManualKernelExecutionConflict
	}
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	s.writeManualNotebookReceipt(w, http.StatusOK, current)
}

func (s *Server) writeManualNotebookReceipt(w http.ResponseWriter, status int, record workspace.ManualKernelExecution) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, s.manualNotebookReceipt(record))
}
