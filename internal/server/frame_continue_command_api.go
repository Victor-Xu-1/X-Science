package server

import (
	"errors"
	"fmt"
	"net/http"

	workspace "x-science/internal/persistence/workspace"
)

func writeFrameContinueError(w http.ResponseWriter, err error) bool {
	if writeConversationAvailabilityError(w, err) {
		return true
	}
	code, status := "", http.StatusConflict
	switch {
	case errors.Is(err, workspace.ErrFrameResumePrecondition):
		code, status = "RESUME_PRECONDITION_REQUIRED", http.StatusPreconditionRequired
	case errors.Is(err, workspace.ErrFrameResumeTargetStale):
		code = "RESUME_TARGET_STALE"
	case errors.Is(err, workspace.ErrFrameResumeCommandUsed):
		code = "RESUME_COMMAND_CONFLICT"
	case errors.Is(err, workspace.ErrFrameResumeCommandBusy):
		code = "RESUME_COMMAND_BUSY"
	default:
		return false
	}
	writeJSON(w, status, map[string]any{"code": code, "detail": err.Error()})
	return true
}

func (s *Server) finishCompatibilityFrameContinue(w http.ResponseWriter, result workspace.ResumeCompatibilityFrameResult) {
	if result.Event == nil || result.ContinueCommand == nil {
		writeV11StoreError(w, errors.New("continue command is missing its durable result"))
		return
	}
	// Publication retries replay immutable receipts. They do not run another
	// wake transaction or reinterpret the command against a newer waiting period.
	if result.WakeEvent != nil {
		if err := s.publishWorkspaceEvent(*result.WakeEvent); err != nil {
			writeV11StoreError(w, fmt.Errorf("continue accepted but wake delivery failed: %w", err))
			return
		}
	}
	if result.NotifyDispatch {
		if err := s.registerFrameResumeDispatch(result.Event.ID, defaultFrameResumeReservationTTL); err != nil {
			writeV11StoreError(w, fmt.Errorf("continue accepted but dispatch notification failed: %w", err))
			return
		}
		if err := s.publishWorkspaceEvent(*result.Event); err != nil {
			writeV11StoreError(w, fmt.Errorf("continue accepted but event delivery failed: %w", err))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root_frame_id":     result.RootFrameID,
		"resumed_frames":    []map[string]any{{"frame_id": result.ResumedFrameID, "agent_name": result.AgentName}},
		"leaf_frames_ready": result.LeafFramesReady, "continue_command": result.ContinueCommand,
	})
}
