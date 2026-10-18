package server

import (
	"context"
	"net/http"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func (s *Server) replayWebConversationMessage(ctx context.Context, intent transcriptstore.FrameMessageIntent) (compatibilityFrameMessageResult, bool, error) {
	replay, found, err := s.workspaceStore.LookupFrameMessageIntentReplay(ctx, intent)
	if err != nil || !found {
		return compatibilityFrameMessageResult{}, false, err
	}
	if replay.State == "accepted" && replay.Root && (replay.FrameStatus == "failed" || replay.FrameStatus == "cancelled") {
		resumed, err := s.workspaceStore.ResumeUnclaimedFrameMessageIntent(ctx, intent)
		if err != nil {
			return compatibilityFrameMessageResult{}, false, err
		}
		if resumed.Event != nil {
			if err := s.registerFrameResumeDispatch(resumed.Event.ID, defaultFrameResumeReservationTTL); err != nil {
				return compatibilityFrameMessageResult{}, false, err
			}
			if err := s.publishWorkspaceEvent(*resumed.Event); err != nil {
				return compatibilityFrameMessageResult{}, false, err
			}
		}
	}
	status := "accepted"
	if replay.State == "queued" {
		status = "message_queued"
	}
	return compatibilityFrameMessageResult{FrameID: intent.FrameID, MessageID: intent.InputMessageID, Status: status}, true, nil
}

// A preflight miss may race the winning admission. Exactly one reread may
// resolve that original proof; no matching fact leaves the actual error intact.
func (s *Server) writeWebConversationSubmissionError(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame, intent *transcriptstore.FrameMessageIntent, original error) {
	if intent != nil {
		result, found, err := s.replayWebConversationMessage(r.Context(), *intent)
		if err != nil {
			writeWebConversationError(w, err)
			return
		}
		if found {
			s.writeWebConversationMessageAccepted(w, r, frame, result, intent, true)
			return
		}
	}
	if isPDFReadingError(original) {
		writePDFReadingError(w, original)
		return
	}
	writeWebConversationError(w, original)
}

// Input acceptance and current execution are separate facts. A lost response
// may be retried after this input has finished or while a newer one is running.
func (s *Server) writeWebConversationMessageAccepted(w http.ResponseWriter, r *http.Request, frame workspace.CompatibilityFrame, result compatibilityFrameMessageResult, intent *transcriptstore.FrameMessageIntent, replayed bool) {
	var receipt map[string]any
	if intent != nil {
		proof, found, err := s.workspaceStore.LookupFrameMessageIntentReplay(r.Context(), *intent)
		if err != nil {
			writeWebConversationError(w, err)
			return
		}
		if found {
			receipt = map[string]any{"version": 1, "frame_id": intent.FrameID, "frame_incarnation_id": intent.FrameIncarnationID,
				"input_message_id": intent.InputMessageID, "request_sha256": intent.RequestSHA256,
				"state": proof.State, "replayed": replayed}
			if proof.State == "accepted" {
				receipt["source"], receipt["event_id"] = "transcript", proof.Snapshot.EventID
				receipt["input_revision"], receipt["branch_id"], receipt["branch_generation"] = proof.Snapshot.InputRevision, proof.Snapshot.BranchID, proof.Snapshot.BranchGeneration
			} else {
				receipt["source"], receipt["event_id"], receipt["queue_sequence"] = "queue", proof.Queue.QueuedEventID, proof.Queue.Sequence
			}
		}
	}
	current, found, err := s.workspaceStore.GetCompatibilityFrame(frame.ID)
	if err != nil {
		writeWebConversationError(w, err)
		return
	}
	if !found {
		writeWebConversationError(w, &webConversationRequestError{Status: http.StatusNotFound, Detail: "conversation not found"})
		return
	}
	runtime, err := s.webConversationRuntimeSnapshot(current)
	if err != nil {
		writeWebConversationError(w, err)
		return
	}
	response := map[string]any{"msg_id": result.MessageID, "turn_id": frame.ID, "runtime": runtime}
	if receipt != nil {
		response["input_receipt"] = receipt
	}
	writeWorkspaceJSON(w, http.StatusAccepted, response)
}
