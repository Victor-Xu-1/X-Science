package server

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
)

// transcriptWebTerminalPresentationReady is the terminal-status publication
// fence. A task can expose a completed/failed/cancelled capsule only after the
// same immutable source boundary is available in the Web message read model.
// This keeps the final answer and its artifact references ordered before the
// terminal status without introducing a second message source.
// Status reads only observe the fence: the existing history/repair owner makes
// it ready. Joining its catch-up would block the bounded runtime snapshot and
// turn ordinary presentation lag into a failed status request.
func (s *Server) transcriptWebTerminalPresentationReady(
	ctx context.Context,
	stream transcriptstore.Stream,
) (bool, error) {
	if s == nil || s.transcriptWebReadModel == nil || s.transcriptStore == nil {
		return true, nil
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return false, err
	}
	fence, err := s.transcriptWebReadModel.GetTranscriptWebProjectionFence(
		ctx, stream.OwnerID, stream.UID, snapshot.BranchID,
	)
	if err != nil {
		return false, err
	}
	return transcriptWebFenceReady(fence), nil
}
