package workspace

import (
	"context"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

// An invalidation of the existing read model, committed with its source CAS.
// It is neither a user message nor a new authority for session bindings.
func (s *Store) enqueuePDFReadingChange(ctx context.Context, tx workspaceTransaction, input PDFReadingMutation, current transcriptstore.PDFReadingContext) error {
	scope := current.Scope
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("x-science:pdf-reading-change:"+scope.FrameID+"\x00"+scope.FrameIncarnationID+"\x00"+input.OperationID)).String()
	_, err := s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
		ID: id, UserID: input.OwnerID, ProjectID: scope.ProjectID, RootFrameID: scope.RootFrameID, FrameID: scope.FrameID,
		Type: "conversation.listChanged", Payload: map[string]any{
			"conversation_id": scope.FrameID, "action": "updated", "source": "xscience",
			"reading_context_changed": true, "frame_incarnation_id": scope.FrameIncarnationID,
			"root_frame_id": scope.RootFrameID, "root_frame_incarnation_id": scope.RootFrameIncarnationID,
			"reading_context_revision": current.Revision,
		},
	}, "", "")
	return err
}
