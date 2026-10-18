package workspace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Materialize only actual durable queue rows into the existing canonical input
// writer. The original queue and claim-consumption clock remain the sole queue
// authority; retries reuse each message's original client identity.
func (s *Store) AdmitKernelChildMessageInputs(ctx context.Context, child KernelSupervisedChild, expected DelegatedStopTarget, messages []KernelChildQueuedMessage) error {
	return transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, project, err := delegatedScopeTx(ctx, tx, child.OwnerUserID, child.FrameID)
		if err != nil {
			return err
		}
		if scope.ParentFrameID != expected.FrameID || scope.ParentFrameIncarnationID != expected.FrameIncarnationID || scope.RootFrameID != child.RootFrameID || project != child.ProjectID {
			return ErrDelegatedStopScopeStale
		}
		current, found, err := readDelegatedChildTx(ctx, tx, child.ParentFrameID, child.FrameID, child.OwnerUserID)
		if err != nil {
			return err
		}
		if !found || current.ToolUseID != child.ToolUseID || current.RootFrameID != child.RootFrameID {
			return ErrDelegatedStopScopeStale
		}
		uid, epoch, _, err := delegatedStreamTx(ctx, tx, child.OwnerUserID, child.FrameID)
		if err != nil {
			return err
		}
		if uid != expected.StreamUID || epoch != expected.StreamEpoch {
			return ErrDelegatedStopScopeStale
		}
		for _, requested := range messages {
			var stored KernelChildQueuedMessage
			err := tx.QueryRowContext(ctx, `SELECT id,frame_id,generation,sender_frame_id,message,kind,created_at
				FROM kernel_child_messages WHERE id=? AND frame_id=?`, requested.ID, child.FrameID).Scan(
				&stored.ID, &stored.FrameID, &stored.Generation, &stored.SenderFrameID, &stored.Message, &stored.Kind, &stored.CreatedAt)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDelegatedStopTargetStale
			}
			if err != nil {
				return err
			}
			if stored.FrameID != requested.FrameID || stored.Generation != requested.Generation || stored.SenderFrameID != requested.SenderFrameID || stored.Message != requested.Message || stored.Kind != requested.Kind {
				return transcriptstore.ErrEventConflict
			}
			identity := "kernel-child-message:" + stored.ID
			origin, inputData, err := s.prepareKernelChildMessageInputTx(ctx, tx, child, expected, stored, identity)
			if err != nil {
				return err
			}
			_, _, _, err = tx.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
				StreamUID: uid, OwnerID: child.OwnerUserID, ClientMessageID: identity,
				FrameEventID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String(), MessageUUID: identity,
				Text: stored.Message, MessageOrigin: origin, Destinations: []string{"ws"},
				InputData: inputData,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
