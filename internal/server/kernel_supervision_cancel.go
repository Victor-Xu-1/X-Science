package server

import (
	"context"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

const kernelBlockingCancellationCleanupTimeout = 5 * time.Second

type kernelBlockingChildOrigin struct {
	child  workspace.KernelSupervisedChild
	target *workspace.DelegatedStopTarget
	err    error
}

func (s *Server) captureBlockingKernelChildOrigins(ctx context.Context, children []workspace.KernelSupervisedChild) []kernelBlockingChildOrigin {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kernelBlockingCancellationCleanupTimeout)
	defer cancel()
	origins := make([]kernelBlockingChildOrigin, len(children))
	for index, child := range children {
		target, err := s.workspaceStore.KernelChildInitialTarget(readCtx, child)
		origins[index] = kernelBlockingChildOrigin{child: child, target: target, err: err}
	}
	return origins
}

func (s *Server) cancelBlockingKernelChildren(ctx context.Context, access workspace.KernelFrameAccess, origins []kernelBlockingChildOrigin, valid []bool) []any {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kernelBlockingCancellationCleanupTimeout)
	defer cancel()
	results := make([]any, len(origins))
	for index, origin := range origins {
		child := origin.child
		if !valid[index] || origin.err != nil || origin.target == nil {
			results[index] = kernelChildWaitFailure(child, "delegated child initial input is unavailable", origin.err)
			continue
		}
		stopped := s.stopKernelChildTarget(cleanupCtx, access, child.FrameID, "blocking delegation was cancelled", "blocking-delegate-cancel:"+child.ToolUseID+":"+child.FrameID, origin.target)
		if stopped["status"] != "stopped" && stopped["status"] != "already_terminal" {
			stopped["frame_id"], stopped["agent_name"], stopped["name"] = child.FrameID, child.AgentName, child.Name
			results[index] = stopped
			continue
		}
		current, found, err := s.workspaceStore.GetKernelSupervisedChild(cleanupCtx, access.Frame.ID, child.FrameID, access.UserID)
		if err == nil && found {
			// Check the original operation after reading the summary. A later
			// incarnation or request must not supply this call's returned output.
			var actual workspace.DelegatedStopTarget
			actual, _, err = s.workspaceStore.KernelChildDispatchTarget(cleanupCtx, access.UserID, access.Frame.ID, child.FrameID)
			if err == nil && (!kernelChildSameInput(origin.target, &actual) || !sameKernelBlockingChildOperation(child, current)) {
				err = workspace.ErrDelegatedStopTargetStale
			}
		}
		if err != nil || !found {
			failure := kernelChildWaitFailure(child, "delegated child cancellation could not be confirmed", err)
			copyKernelChildStopObservation(failure, stopped)
			results[index] = failure
			continue
		}
		result := kernelChildResult(current)
		copyKernelChildStopObservation(result, stopped)
		results[index] = result
	}
	// Stop retains its landing notification. The ordinary frame-only collector
	// cannot atomically fence this cancellation's original input/incarnation.
	return results
}

func sameKernelBlockingChildOperation(original, current workspace.KernelSupervisedChild) bool {
	return original.FrameID == current.FrameID && original.ParentFrameID == current.ParentFrameID &&
		original.RootFrameID == current.RootFrameID && original.ProjectID == current.ProjectID &&
		original.OwnerUserID == current.OwnerUserID && original.ToolUseID == current.ToolUseID &&
		original.StartedAt.Equal(current.StartedAt)
}

func kernelChildWaitFailure(child workspace.KernelSupervisedChild, message string, err error) map[string]any {
	if err != nil {
		message += ": " + err.Error()
	}
	return map[string]any{
		"frame_id": child.FrameID, "agent_name": child.AgentName, "name": child.Name,
		"status": "failed", "execution_observation": "unavailable", "error": message,
	}
}

func copyKernelChildStopObservation(result, stopped map[string]any) {
	for _, key := range []string{"request_id", "execution_observation"} {
		if value, found := stopped[key]; found {
			result[key] = value
		}
	}
	if message, ok := stopped["error"].(string); ok && message != "" {
		if previous, _ := result["error"].(string); previous != "" && previous != message {
			message = previous + "; " + message
		}
		result["error"] = message
	}
}
