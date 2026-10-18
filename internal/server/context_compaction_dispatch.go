package server

import (
	"context"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Validate maintenance identity before generic dispatch convergence can attach
// an old dispatch to a newer ordinary runner. The final claim repeats the exact
// admitted input fence; this preflight is not execution authority.
func (s *Server) checkContextCompactionDispatch(ctx context.Context, dispatch workspace.CompatibilityFrameResumeDispatch) error {
	request, err := workspace.ContextCompactionDispatchRequest(dispatch)
	if err != nil || request == nil {
		return err
	}
	access, found, err := s.workspaceStore.GetFrameRealtimeContext(dispatch.FrameID)
	if err != nil {
		return err
	}
	if !found {
		return transcriptstore.ErrContextCompactionUnavailable
	}
	receipt, err := s.transcriptStore.ReadContextCompaction(ctx, access.UserID, dispatch.FrameID, *request)
	if err != nil {
		return err
	}
	stream, err := s.transcriptStore.GetStream(ctx, request.Expected.StreamUID, access.UserID)
	if err != nil {
		return err
	}
	if receipt.State == "cancelled" || receipt.TerminalEventID > 0 || stream.InputRevision != receipt.InputRevision {
		return transcriptstore.ErrClaimStale
	}
	return nil
}

// A stale maintenance dispatch can only fence its original operation/dispatch.
// It cannot enter generic requeue, select the latest input, or project failure
// onto the Frame now owned by another prompt.
func (s *Server) settleStaleContextCompactionDispatch(ctx context.Context, dispatch workspace.CompatibilityFrameResumeDispatch, cause error) (string, bool, error) {
	if !errors.Is(cause, transcriptstore.ErrClaimStale) {
		return "", false, nil
	}
	request, err := workspace.ContextCompactionDispatchRequest(dispatch)
	if err != nil || request == nil {
		return "", false, err
	}
	access, found, err := s.workspaceStore.GetFrameRealtimeContext(dispatch.FrameID)
	if err != nil {
		return "", true, err
	}
	if !found {
		return "", true, transcriptstore.ErrContextCompactionUnavailable
	}
	receipt, err := s.workspaceStore.CancelContextCompaction(ctx, access.UserID, dispatch.FrameID, *request)
	status := receipt.State
	if status == "committed" {
		status = "completed"
	}
	return status, true, err
}
