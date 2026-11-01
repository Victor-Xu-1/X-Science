package server

import (
	"context"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

type nativeFileReadObservation struct{ confirmed bool }
type nativeFileReadObservationKey struct{}

// Only the original authorized regular-file read can confirm this private
// invocation witness. Version materialization and recovery paths remain
// unknown; model result fields and read-only tool annotations grant nothing.
func (s *Server) observeNativeFileRead(ctx context.Context, access workspace.KernelFrameAccess, response any) {
	observation, _ := ctx.Value(nativeFileReadObservationKey{}).(*nativeFileReadObservation)
	if observation == nil || context.Cause(ctx) != nil {
		return
	}
	value, _ := unwrapAgentRuntimeRichToolResponse(response)
	if mapValue(value)["_policy_skip"] == true || agentruntime.ClassifyToolResult(value) != agentruntime.ToolResultSucceeded || agentruntime.ToolResultDidNotExecute(value) {
		return
	}
	// Reuse the existing canonical live actor/lease authority; do not create a
	// second reader owner or trust mutable task metadata. The actual handler's
	// freshly validated access must identify this same running frame/root.
	owner := s.currentBackgroundShellOwner(ctx)
	if owner == nil || owner.UserID != access.UserID || owner.ProjectID != access.Frame.ProjectID ||
		owner.FrameID != access.Frame.ID || owner.FrameIncarnationID != access.Frame.IncarnationID ||
		owner.RootFrameID != access.Frame.RootFrameID || owner.RootFrameIncarnationID != access.RootFrameIncarnationID {
		return
	}
	observation.confirmed = true
}
