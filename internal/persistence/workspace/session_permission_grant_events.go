package workspace

import "context"

// This is only a read invalidation. Grant rows and their exact generations
// remain the authority; no runtime status or approval completion is invented.
func (s *Store) enqueueSessionPermissionGrantChangedTx(ctx context.Context, tx workspaceTransaction, owner string, scope SessionPermissionScope, eventID string) error {
	_, err := s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{
		ID: eventID, UserID: owner, ProjectID: scope.ProjectID,
		RootFrameID: scope.RootFrameID, FrameID: scope.RootFrameID,
		Type: "conversation.listChanged", Payload: map[string]any{
			"conversation_id": scope.RootFrameID, "action": "updated", "source": "xscience",
			"permission_grants_changed": true, "root_frame_id": scope.RootFrameID,
			"root_frame_incarnation_id": scope.RootFrameIncarnationID,
		},
	}, "", "")
	return err
}
