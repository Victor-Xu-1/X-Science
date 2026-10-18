package workspace

import (
	"context"
	"database/sql"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) CancelContextCompaction(ctx context.Context, owner, frame string, request transcriptstore.ContextCompactionRequest) (transcriptstore.ContextCompactionReceipt, error) {
	var result transcriptstore.ContextCompactionReceipt
	repo, err := s.TranscriptRepository(ctx)
	if err != nil {
		return result, err
	}
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		result, err = tx.CancelContextCompaction(ctx, owner, frame, request)
		if err != nil {
			return err
		}
		status := frameResumeDispatchCancelled
		if result.State == "committed" {
			status = frameResumeDispatchCompleted
		} else if result.State == "failed" {
			status = frameResumeDispatchFailed
		}
		if err = settleContextCompactionDispatchTx(ctx, tx, frame, request, status, s.now().UTC()); err != nil {
			return err
		}
		if result.Replayed || result.CancellationStatus == "already_terminal" {
			return nil
		}
		intent := request.MessageIntent(owner, frame)
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT frame_id FROM queued_user_messages WHERE intent_id=?`, intent.InputMessageID).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			return transcriptstore.ErrMessageIntentConflict
		}
		cancelled := transcriptstore.FrameMessageIntentCancellation{Intent: intent, Accepted: result.Accepted, Cancelled: true, CancellationStatus: result.CancellationStatus}
		delivery := map[string]any{"messageIntent": intent, "message_intent_cancellation": cancelled, "context_compaction_request": request}
		raw, err := marshalCompatibilityQueueEnvelope(map[string]any{"messageIntent": intent}, delivery, "")
		if err != nil {
			return err
		}
		now := s.now().UTC()
		_, err = tx.ExecContext(ctx, `INSERT INTO queued_user_messages(sequence,frame_id,payload,intent_id,state,resolved_at,created_at)
			SELECT COALESCE(MAX(sequence),0)+1,?,?,?,'retracted',?,? FROM queued_user_messages`, frame, raw, intent.InputMessageID, now, now)
		if err != nil || result.Attempt > 0 {
			return err
		}
		var project string
		if err = tx.QueryRowContext(ctx, `SELECT project_id FROM frames WHERE id=?`, frame).Scan(&project); err != nil {
			return err
		}
		_, err = s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{ID: "context-compaction-cancelled:" + request.RequestID,
			UserID: owner, ProjectID: project, RootFrameID: request.Expected.RootFrameID, FrameID: frame, Type: "compaction_status",
			Payload: map[string]any{"schema": "x-science.context-compaction-event.v1", "conversation_id": frame, "request_id": request.RequestID,
				"frame_incarnation_id": request.Expected.FrameIncarnationID, "root_frame_id": request.Expected.RootFrameID,
				"root_frame_incarnation_id": request.Expected.RootFrameIncarnationID, "state": "cancelled"}}, "", "")
		return err
	})
	if err == nil {
		s.signalKernelRetentionWake()
	}
	return result, err
}
