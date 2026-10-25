package server

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

// SDK and browser callers share the same canonical cancellation and exact
// physical signal path. Stop does not collect/discard a child's output.
func (s *Server) stopKernelChild(ctx context.Context, access workspace.KernelFrameAccess, frameID, reason string, callIDs ...string) map[string]any {
	callID := uuid.NewString()
	if len(callIDs) == 1 && callIDs[0] != "" {
		callID = callIDs[0]
	}
	return s.stopKernelChildTarget(ctx, access, frameID, reason, callID, nil)
}

func (s *Server) stopKernelChildTarget(ctx context.Context, access workspace.KernelFrameAccess, frameID, reason, callID string, expected *workspace.DelegatedStopTarget) map[string]any {
	child, found, err := s.workspaceStore.GetKernelSupervisedChild(ctx, access.Frame.ID, frameID, access.UserID)
	if err == nil && !found && expected == nil && s.kernelManager != nil {
		// Retained SDK-only execution-ID overload. Browser routes never enter it.
		interrupted := s.kernelManager.Interrupt(access.Frame.ID, frameID)
		if interrupted.Interrupted || interrupted.Dequeued {
			return map[string]any{"exec_id": frameID, "status": "stopped", "outcome": "interrupted", "via": interrupted.Via, "dequeued": interrupted.Dequeued}
		}
	}
	if err != nil || !found {
		return map[string]any{"child_frame_id": frameID, "status": "failed", "error": "delegated child is unavailable"}
	}
	scope := workspace.DelegatedStopScope{ParentFrameID: access.Frame.ID, ParentFrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID}
	receipt, err := s.workspaceStore.AdmitKernelChildStop(ctx, access.UserID, scope, frameID, reason, callID, expected)
	if err != nil {
		return map[string]any{"child_frame_id": frameID, "status": "failed", "error": err.Error()}
	}
	done, err := s.signalDelegatedStop(ctx, access.UserID, &receipt)
	if err != nil {
		return map[string]any{"child_frame_id": frameID, "status": "stopping", "execution_observation": "unavailable", "request_id": receipt.Request.RequestID, "error": err.Error()}
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-waitCtx.Done():
	}
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer readCancel()
	observed, observedFound, observationErr := s.readDelegatedStopReceipt(readCtx, access.UserID, access.Frame.ID, receipt.Request.RequestID)
	if observationErr == nil && observedFound {
		receipt = observed
	} else if observationErr == nil {
		observationErr = workspace.ErrDelegatedStopUnavailable
	}
	result := map[string]any{"child_frame_id": frameID, "frame_id": frameID, "agent_name": child.AgentName,
		"status": "stopping", "execution_observation": "unavailable", "request_id": receipt.Request.RequestID}
	descendants := []any{}
	for _, outcome := range receipt.Targets {
		if outcome.Target.FrameID != frameID {
			descendants = append(descendants, map[string]any{"frame_id": outcome.Target.FrameID, "outcome": outcome.Outcome, "execution_observation": outcome.ExecutionObservation})
			continue
		}
		result["execution_observation"] = outcome.ExecutionObservation
		if outcome.Outcome == "already_terminal" {
			result["status"], result["child_status"], result["output_data"] = "already_terminal", kernelChildPublicStatus(child), child.Output
			result["message"] = fmt.Sprintf("Child is already %s; nothing to stop.", kernelChildPublicStatus(child))
		} else {
			result["output_data"] = map[string]any{"stopped_by_parent": true, "reason": reason}
			if outcome.Outcome == "cancelled" && receipt.State == "settled" {
				result["status"] = "stopped"
			}
		}
	}
	result["stopped_descendants"] = descendants
	if observationErr != nil {
		result["status"], result["execution_observation"] = "stopping", "unavailable"
		result["error"], result["diagnostic"] = observationErr.Error(), "observation_persistence_pending"
	}
	return result
}
