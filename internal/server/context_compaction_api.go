package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) routeContextCompaction(w http.ResponseWriter, r *http.Request) bool {
	parts := workspacePathSegments(strings.TrimPrefix(r.URL.Path, "/api/conversations/"))
	if len(parts) < 2 || parts[1] != "context-compactions" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	owner := strings.TrimSpace(r.Header.Get("X-Science-User-Id"))
	if owner == "" {
		writeWorkspaceJSON(w, 401, map[string]any{"code": "AUTH_REQUIRED"})
		return true
	}
	frame, err := url.PathUnescape(parts[0])
	if err != nil || frame == "" {
		writeContextCompactionError(w, transcriptstore.ErrContextCompactionInvalid)
		return true
	}
	if s.transcriptStore == nil || s.workspaceStore == nil {
		writeContextCompactionError(w, errors.New("storage unavailable"))
		return true
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		if len(r.URL.Query()) != 0 {
			writeContextCompactionError(w, transcriptstore.ErrContextCompactionInvalid)
			return true
		}
		result, err := s.transcriptStore.ContextCompactionSnapshot(r.Context(), owner, frame)
		if err != nil {
			writeContextCompactionError(w, err)
			return true
		}
		if result.Operation != nil {
			s.observeContextCompaction(result.Operation)
		}
		writeWorkspaceJSON(w, 200, result)
		return true
	}
	byID := len(parts) == 4 && parts[2] == "by-request"
	if byID {
		if _, err := uuid.Parse(parts[3]); err != nil {
			writeContextCompactionError(w, transcriptstore.ErrContextCompactionInvalid)
			return true
		}
	}
	if byID && r.Method == http.MethodGet {
		expected, err := contextCompactionExpectedQuery(r.URL.Query())
		if err != nil {
			writeContextCompactionError(w, err)
			return true
		}
		request, err := s.transcriptStore.ContextCompactionRequestByID(r.Context(), owner, frame, parts[3], expected)
		if err != nil {
			writeContextCompactionError(w, err)
			return true
		}
		receipt, err := s.transcriptStore.ReadContextCompaction(r.Context(), owner, frame, request)
		if err != nil {
			writeContextCompactionError(w, err)
			return true
		}
		s.observeContextCompaction(&receipt)
		writeWorkspaceJSON(w, 200, receipt)
		return true
	}
	if len(parts) == 2 && r.Method == http.MethodPost || byID && r.Method == http.MethodDelete {
		var request transcriptstore.ContextCompactionRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		var trailing any
		if len(r.URL.Query()) != 0 || decoder.Decode(&request) != nil || decoder.Decode(&trailing) != io.EOF || request.Validate() != nil || byID && parts[3] != request.RequestID {
			writeContextCompactionError(w, transcriptstore.ErrContextCompactionInvalid)
			return true
		}
		var receipt transcriptstore.ContextCompactionReceipt
		if r.Method == http.MethodPost {
			receipt, err = s.workspaceStore.AdmitContextCompaction(r.Context(), owner, frame, request)
		} else {
			receipt, err = s.cancelContextCompactionAndDrain(r.Context(), owner, frame, request)
		}
		if err != nil {
			writeContextCompactionError(w, err)
			return true
		}
		s.signalTranscriptWebDelivery()
		s.observeContextCompaction(&receipt)
		status := http.StatusAccepted
		if r.Method == http.MethodDelete && receipt.CanSubmitNextInput {
			status = http.StatusOK
		}
		writeWorkspaceJSON(w, status, receipt)
		return true
	}
	writeWorkspaceJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "METHOD_NOT_ALLOWED"})
	return true
}

func contextCompactionExpectedQuery(query url.Values) (transcriptstore.ContextCompactionTarget, error) {
	var target transcriptstore.ContextCompactionTarget
	keys := []string{"expected_frame_incarnation_id", "expected_root_frame_id", "expected_root_frame_incarnation_id", "expected_stream_uid", "expected_stream_epoch"}
	if len(query) != len(keys) {
		return target, transcriptstore.ErrContextCompactionInvalid
	}
	for _, key := range keys {
		if values := query[key]; len(values) != 1 || values[0] == "" || len(values[0]) > 512 {
			return target, transcriptstore.ErrContextCompactionInvalid
		}
	}
	epoch, err := strconv.ParseInt(query.Get(keys[4]), 10, 64)
	if err != nil || epoch <= 0 {
		return target, transcriptstore.ErrContextCompactionInvalid
	}
	target.FrameIncarnationID, target.RootFrameID, target.RootFrameIncarnationID = query.Get(keys[0]), query.Get(keys[1]), query.Get(keys[2])
	target.StreamUID, target.StreamEpoch = query.Get(keys[3]), epoch
	return target, nil
}

func writeContextCompactionError(w http.ResponseWriter, err error) {
	status, code := 503, "CONTEXT_COMPACTION_STORAGE_UNAVAILABLE"
	switch {
	case errors.Is(err, transcriptstore.ErrContextCompactionInvalid):
		status, code = 400, "CONTEXT_COMPACTION_REQUEST_INVALID"
	case errors.Is(err, transcriptstore.ErrContextCompactionUnavailable), errors.Is(err, transcriptstore.ErrOwnerMismatch):
		status, code = 404, "CONTEXT_COMPACTION_TARGET_UNAVAILABLE"
	case errors.Is(err, transcriptstore.ErrContextCompactionNotFound):
		status, code = 404, "CONTEXT_COMPACTION_NOT_FOUND"
	case errors.Is(err, transcriptstore.ErrContextCompactionStale), errors.Is(err, transcriptstore.ErrClaimStale):
		status, code = 409, "CONTEXT_COMPACTION_SCOPE_STALE"
	case errors.Is(err, transcriptstore.ErrContextCompactionBusy):
		status, code = 409, "CONTEXT_COMPACTION_BUSY"
	case errors.Is(err, transcriptstore.ErrContextCompactionConflict), errors.Is(err, transcriptstore.ErrMessageIntentConflict), errors.Is(err, transcriptstore.ErrEventConflict):
		status, code = 409, "CONTEXT_COMPACTION_REQUEST_CONFLICT"
	case errors.Is(err, transcriptstore.ErrMessageIntentCancelled):
		status, code = 409, "CONTEXT_COMPACTION_CANCELLED"
	}
	writeWorkspaceJSON(w, status, map[string]any{"code": code, "message": "Context compaction could not be completed."})
}
