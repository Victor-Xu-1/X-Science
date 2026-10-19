package workspace

import (
	"context"
	"strconv"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

// The resolver's outcome and its list invalidation share the existing reliable
// outbox transaction. A dropped websocket/HTTP acknowledgement cannot invent a
// second owner or lose the subsequent current-state readback.
func (s *Store) RecordSessionSpecialistPreparation(ctx context.Context,claim transcriptstore.RunnerClaim,snapshotID string,applied bool) error {
	return transcriptstore.NewRepository(s.db).RecordClaimedRunnerAgentPreparation(ctx,claim,snapshotID,applied,func(tx *transcriptstore.ImmediateTransaction,binding sessionagent.Binding)error {
		var project string
		if err:=tx.QueryRowContext(ctx,`SELECT project_id FROM frames WHERE id=? AND incarnation_id=?`,binding.Scope.FrameID,binding.Scope.FrameIncarnationID).Scan(&project); err!=nil { return err }
		status:="failed"
		if applied { status="applied" }
		_,err:=s.enqueueRealtimeOutboxTransaction(ctx,tx,RealtimeEventInput{
			ID:sessionagent.RecordID("application-notification",claim.OwnerID,binding.Scope.FrameIncarnationID,claim.StreamUID,strconv.FormatInt(claim.Attempt,10),snapshotID,status),
			UserID:claim.OwnerID,ProjectID:project,RootFrameID:binding.Scope.RootFrameID,FrameID:binding.Scope.FrameID,Type:"conversation.listChanged",
			Payload:map[string]any{"conversation_id":binding.Scope.FrameID,"action":"updated","source":"xscience","specialist_changed":true,
				"frame_incarnation_id":binding.Scope.FrameIncarnationID,"specialist_revision":binding.Selection.Revision},
		},"","")
		return err
	})
}
