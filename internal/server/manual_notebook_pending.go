package server

import (
	"net/http"
	"net/url"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

type manualNotebookPendingSummary struct {
	RequestID    string               `json:"request_id"`
	ExecutionID  string               `json:"execution_id"`
	RequestKind  string               `json:"request_kind"`
	Target       manualNotebookTarget `json:"target"`
	State        string               `json:"state"`
	StateVersion int64                `json:"state_version"`
	AcceptedAt   time.Time            `json:"accepted_at"`
}

func (s *Server) handleManualNotebookPending(w http.ResponseWriter, r *http.Request) {
	if !manualNotebookRequireActor(w, r) {
		return
	}
	expected, cursor, err := decodeManualNotebookPending(r)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	access, err := s.manualNotebookAccess(r, expected)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	page, err := s.workspaceStore.ListManualKernelPending(r.Context(), access, cursor)
	if err != nil {
		writeManualNotebookAPIError(w, err)
		return
	}
	summaries := make([]manualNotebookPendingSummary, 0, len(page.Records))
	for _, record := range page.Records {
		summaries = append(summaries, manualNotebookPendingSummary{RequestID: record.Request.RequestID,
			ExecutionID: record.ExecutionID, RequestKind: manualNotebookRequestKind(record), Target: manualNotebookTargetFromRecord(record),
			State: record.State, StateVersion: record.StateVersion, AcceptedAt: record.AcceptedAt})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"schema": "x-science.manual-kernel-pending.v1", "pending": summaries,
		"next_cursor": page.NextCursor, "scan_complete": page.ScanComplete, "through_sequence": page.ThroughSequence})
}

func decodeManualNotebookPending(r *http.Request) (manualNotebookExpectedTarget, string, error) {
	var expected manualNotebookExpectedTarget
	var cursor string
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return expected, cursor, manualNotebookInvalid("Invalid discovery query")
	}
	fields := map[string]*string{"expected_frame_incarnation_id": &expected.FrameIncarnationID,
		"expected_root_frame_id": &expected.RootFrameID, "expected_root_frame_incarnation_id": &expected.RootIncarnationID,
		"cursor": &cursor}
	for key, values := range query {
		field, ok := fields[key]
		if !ok || len(values) != 1 {
			return expected, cursor, manualNotebookInvalid("Unknown or duplicate discovery parameter")
		}
		*field = values[0]
	}
	for _, value := range []string{expected.FrameIncarnationID, expected.RootFrameID, expected.RootIncarnationID} {
		if !validManualNotebookID(value) {
			return expected, cursor, manualNotebookInvalid("Complete frozen discovery scope is required")
		}
	}
	if len(cursor) > 8192 {
		return expected, cursor, manualNotebookInvalid("Invalid discovery cursor")
	}
	return expected, cursor, nil
}

func manualNotebookTargetFromRecord(record workspace.ManualKernelExecution) manualNotebookTarget {
	return manualNotebookTarget{Schema: "x-science.manual-kernel-target.v1", FrameID: record.FrameID, RootFrameID: record.RootFrameID,
		FrameIncarnationID: record.FrameIncarnationID, RootIncarnationID: record.RootIncarnationID,
		KernelID: record.KernelID, KernelGeneration: record.KernelGeneration,
		Language: record.Request.Language, Environment: record.Request.Environment}
}
