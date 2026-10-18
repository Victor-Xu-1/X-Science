package server

import (
	"context"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) publishContextCompactionDelivery(frame workspace.FrameRealtimeContext, id string, payload map[string]any, eventType string) error {
	var request *transcriptstore.ContextCompactionRequest
	state := "accepted"
	if eventType == "user_message" {
		var err error
		request, err = transcriptstore.ContextCompactionRequestFromPayload(payload)
		if err != nil {
			return err
		}
	} else {
		raw, err := json.Marshal(payload["maintenance"])
		var value transcriptstore.ContextCompactionRequest
		if err != nil || json.Unmarshal(raw, &value) != nil || value.Validate() != nil {
			return transcriptstore.ErrEventConflict
		}
		request = &value
		state = "compacting"
		if eventType == "runner_finished" {
			state = webString(payload["status"])
		}
		if state == "completed" || transcriptstore.IsCompactionBoundaryPayload(payload) {
			state = "committed"
		}
	}
	if request == nil {
		return transcriptstore.ErrEventConflict
	}
	if frame.Frame.IncarnationID != request.Expected.FrameIncarnationID || frame.Frame.RootFrameID != request.Expected.RootFrameID {
		return nil
	}
	if _, err := s.transcriptStore.ReadContextCompaction(context.Background(), frame.UserID, frame.Frame.ID, *request); err != nil {
		if errors.Is(err, transcriptstore.ErrContextCompactionStale) || errors.Is(err, transcriptstore.ErrContextCompactionUnavailable) {
			return nil
		}
		return err
	}
	return s.publishWebFrameEvent(frame, id+":compaction", "compaction_status", map[string]any{
		"schema": "x-science.context-compaction-event.v1", "conversation_id": frame.Frame.ID, "request_id": request.RequestID,
		"frame_incarnation_id": request.Expected.FrameIncarnationID, "root_frame_id": request.Expected.RootFrameID,
		"root_frame_incarnation_id": request.Expected.RootFrameIncarnationID, "state": state,
	})
}
