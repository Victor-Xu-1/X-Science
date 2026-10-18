package workspace

import (
	"context"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) AdmitContextCompaction(ctx context.Context, owner, frame string, request transcriptstore.ContextCompactionRequest) (transcriptstore.ContextCompactionReceipt, error) {
	var result transcriptstore.ContextCompactionReceipt
	repo, err := s.TranscriptRepository(ctx)
	if err != nil {
		return result, err
	}
	err = repo.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var created bool
		var err error
		result, created, err = tx.AdmitContextCompaction(ctx, owner, frame, request)
		if err != nil || !created {
			return err
		}
		var agent, project, root string
		if err = tx.QueryRowContext(ctx, `SELECT agent_name,project_id,root_frame_id FROM frames WHERE id=?`, frame).Scan(&agent, &project, &root); err != nil {
			return err
		}
		now := s.now().UTC()
		event, err := appendCompatibilityDispatchAuditEvent(ctx, tx, "context-compaction-dispatch:"+request.RequestID, frame, "frame_resumed", map[string]any{
			"contextCompactionRequest": request,
			"previousStatus":           "idle", "rootFrameId": root, "leafFramesReady": 1, "agentName": agent,
			"dispatch": map[string]any{"status": frameResumeDispatchRegistered, "attempt": 0, "registeredAt": now.Format(time.RFC3339Nano), "autoResume": true, "reasonCode": "manual_context_compaction"},
		}, now)
		if err != nil {
			return err
		}
		_, err = s.enqueueRealtimeOutboxTransaction(ctx, tx, RealtimeEventInput{ID: "context-compaction-dispatch-notice:" + request.RequestID,
			UserID: owner, ProjectID: project, RootFrameID: root, FrameID: frame, Type: "frame_update", Payload: map[string]any{"status": "processing"}}, event.ID, "")
		return err
	})
	if err == nil {
		s.signalKernelRetentionWake()
	}
	return result, err
}
