package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// State-change wakeups use the existing queue and recovery scan. There is no
// second retry worker: the same dispatch is rearmed only by new durable facts
// or a changed runtime contract, never by the passage of time.
func (s *Server) wakeChangedFrameRecoveryWaits(ctx context.Context) error {
	const pageSize = 100
	after := ""
	for {
		page, err := s.workspaceStore.ListRecoveryConditionWaitDispatches(ctx, after, pageSize)
		if err != nil {
			return err
		}
		for _, waiting := range page {
			after = waiting.ResumeEvent.ID
			current, found, err := s.workspaceStore.GetCompatibilityFrameResumeDispatchByFrame(waiting.FrameID)
			if err != nil {
				return err
			}
			if !found || current.ResumeEvent.ID != waiting.ResumeEvent.ID || current.WaitingFor != workspace.CompatibilityFrameResumeDispatchWaitRecoveryCondition {
				continue
			}
			woken, err := s.wakeFrameRecoveryAfterKernelCompletion(ctx, current.ResumeEvent.ID, current.WaitRef)
			if errors.Is(err, transcriptstore.ErrConversationArchiveInvalid) {
				// Preserve the malformed conversation's execution fence without
				// starving unrelated roots later in the same recovery page.
				log.Printf("runner_recovery_wait_unavailable frame=%q reason=invalid_conversation_archive", current.FrameID)
				continue
			}
			if err != nil {
				return err
			}
			if woken {
				continue
			}
			cause, err := s.frameRecoveryWaitStateChange(ctx, current)
			if err != nil {
				return err
			}
			if cause == nil {
				continue
			}
			event, woken, err := s.workspaceStore.WakeCompatibilityFrameResumeDispatch(workspace.FrameResumeDispatchWakeInput{
				ResumeEventID: current.ResumeEvent.ID, ExpectedWaitRef: current.WaitRef,
				Source: workspace.FrameResumeWakeRecoveryCondition, Cause: *cause,
			})
			if err != nil {
				return err
			}
			if woken {
				if err := s.publishWorkspaceEvent(event); err != nil {
					return err
				}
				s.signalFrameResumeDispatchForEvent(event.Type)
				log.Printf("runner_recovery_condition_changed frame=%q action=wake_existing_dispatch", current.FrameID)
			}
		}
		if len(page) < pageSize {
			return nil
		}
	}
}

// Use the same durable join after parking and during recovery. A completion
// notification can precede the wait commit, so realtime delivery alone cannot
// establish that the runner has observed the result.
func (s *Server) wakeFrameRecoveryAfterKernelCompletion(ctx context.Context, resumeEventID, waitRef string) (bool, error) {
	event, woken, err := s.workspaceStore.WakeRecoveryConditionAfterKernelCompletion(ctx, resumeEventID, waitRef)
	if err != nil || !woken {
		return woken, err
	}
	if err := s.publishWorkspaceEvent(event); err != nil {
		return true, err
	}
	s.signalFrameResumeDispatchForEvent(event.Type)
	log.Printf("runner_recovery_kernel_completion frame=%q action=wake_existing_dispatch", event.FrameID)
	return true, nil
}

func (s *Server) frameRecoveryWaitStateChange(ctx context.Context, dispatch workspace.CompatibilityFrameResumeDispatch) (*workspace.FrameResumeWakeCause, error) {
	state := mapValue(dispatch.ResumeEvent.Payload["dispatch"])
	revision := int(numberValue(state["recoveryContractRevision"]))
	if revision > 0 && revision < sessionRunnerRecoveryContractRevision {
		cause := frameResumeRecoveryContractWakeCause(revision)
		return &cause, nil
	}
	checkpoint := int64(numberValue(state["checkpointEventId"]))
	frame, found, err := s.workspaceStore.GetFrameRealtimeContext(dispatch.FrameID)
	if err != nil || !found {
		return nil, err
	}
	stream, found, err := s.transcriptStore.GetFrameStreamBySession(ctx, frame.UserID, dispatch.FrameID)
	if err != nil || !found {
		return nil, err
	}
	if checkpoint <= 0 || stream.NextEventID <= checkpoint+1 {
		return nil, nil
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return nil, err
	}
	var cause *workspace.FrameResumeWakeCause
	err = s.scanTranscriptProjection(ctx, snapshot, stream.OwnerID, snapshot.ThroughPublicationSequence, func(event transcriptstore.ProjectedEvent) error {
		if cause != nil || event.Event.EventID <= checkpoint {
			return nil
		}
		switch event.Event.Type {
		case "user_message", "user_input_response":
			cause = frameRecoveryWaitEventCause(event.Event)
		case "runner_checkpoint":
			var message eventjournal.Message
			raw := event.ResolvedPayloadJSON
			if len(raw) == 0 {
				raw = event.Event.PayloadJSON
			}
			if err := json.Unmarshal(raw, &message); err != nil {
				return err
			}
			message["type"] = "runner_checkpoint"
			if runnerCheckpointHasMaterialProgress(message) {
				cause = frameRecoveryWaitEventCause(event.Event)
			}
		}
		return nil
	})
	return cause, err
}

func frameRecoveryWaitEventCause(event transcriptstore.Event) *workspace.FrameResumeWakeCause {
	return &workspace.FrameResumeWakeCause{Kind: "recovery_condition_event", Evidence: "canonical",
		ReferenceID: fmt.Sprintf("%s:%d", event.StreamUID, event.EventID), StreamUID: event.StreamUID, EventID: event.EventID}
}
