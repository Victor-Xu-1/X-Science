package server

import (
	"context"
	"sync"
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
	origins := make([]kernelBlockingChildOrigin, len(children))
	var reads sync.WaitGroup
	reads.Add(len(children))
	for index, child := range children {
		go func(index int, child workspace.KernelSupervisedChild) {
			defer reads.Done()
			origins[index] = s.captureBlockingKernelChildOrigin(ctx, child)
		}(index, child)
	}
	reads.Wait()
	return origins
}

func (s *Server) captureBlockingKernelChildOrigin(ctx context.Context, child workspace.KernelSupervisedChild) kernelBlockingChildOrigin {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kernelBlockingCancellationCleanupTimeout)
	defer cancel()
	target, err := s.workspaceStore.KernelChildInitialTarget(readCtx, child)
	return kernelBlockingChildOrigin{child: child, target: target, err: err}
}

func (s *Server) cancelBlockingKernelChildren(ctx context.Context, access workspace.KernelFrameAccess, origins []kernelBlockingChildOrigin, valid []bool) []any {
	results := make([]any, len(origins))
	var stops sync.WaitGroup
	stops.Add(len(origins))
	for index, origin := range origins {
		go func(index int, origin kernelBlockingChildOrigin) {
			defer stops.Done()
			results[index] = s.cancelBlockingKernelChild(ctx, access, origin, valid[index])
		}(index, origin)
	}
	stops.Wait()
	// Stop retains its landing notification. The ordinary frame-only collector
	// cannot atomically fence this cancellation's original input/incarnation.
	return results
}

func (s *Server) cancelBlockingKernelChild(ctx context.Context, access workspace.KernelFrameAccess, origin kernelBlockingChildOrigin, valid bool) map[string]any {
	child := origin.child
	if !valid || origin.err != nil || origin.target == nil {
		return kernelChildWaitFailure(child, "delegated child initial input is unavailable", origin.err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kernelBlockingCancellationCleanupTimeout)
	defer cancel()
	// Each accepted child gets its own bounded work. A sibling's physical join
	// must not consume this child's budget or delay its exact cancellation signal.
	stopped := s.stopKernelChildTarget(cleanupCtx, access, child.FrameID, "blocking delegation was cancelled", "blocking-delegate-cancel:"+child.ToolUseID+":"+child.FrameID, origin.target)
	if stopped["status"] != "stopped" && stopped["status"] != "already_terminal" {
		stopped["frame_id"], stopped["agent_name"], stopped["name"] = child.FrameID, child.AgentName, child.Name
		return stopped
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
		return failure
	}
	result := kernelChildResult(current)
	copyKernelChildStopObservation(result, stopped)
	return result
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
