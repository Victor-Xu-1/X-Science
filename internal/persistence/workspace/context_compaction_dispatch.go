package workspace

import (
	"context"
	"encoding/json"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func ContextCompactionDispatchRequest(dispatch CompatibilityFrameResumeDispatch) (*transcriptstore.ContextCompactionRequest, error) {
	value, found := dispatch.ResumeEvent.Payload["contextCompactionRequest"]
	if !found {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	var request transcriptstore.ContextCompactionRequest
	if err != nil || json.Unmarshal(raw, &request) != nil || request.Validate() != nil {
		return nil, transcriptstore.ErrContextCompactionInvalid
	}
	return &request, nil
}

func settleContextCompactionDispatchTx(ctx context.Context, tx workspaceTransaction, frame string, request transcriptstore.ContextCompactionRequest, status string, now time.Time) error {
	row, found, err := scanCompatibilityFrameResumeDispatch(tx.QueryRowContext(ctx, `SELECT e.id,e.frame_id,e.sequence,e.payload,e.created_at,
		f.root_frame_id,f.project_id,f.agent_name,f.status FROM frame_events e JOIN frames f ON f.id=e.frame_id
		WHERE e.id=? AND e.frame_id=? AND e.event_type='frame_resumed'`, "context-compaction-dispatch:"+request.RequestID, frame))
	if err != nil || !found {
		return err
	}
	stored, err := ContextCompactionDispatchRequest(row.dispatch)
	if err != nil {
		return err
	}
	if stored == nil || *stored != request {
		return transcriptstore.ErrContextCompactionConflict
	}
	if isCompatibilityResumeDispatchTerminal(row.dispatch.Status) {
		return nil
	}
	payload := row.dispatch.ResumeEvent.Payload
	dispatchPayload := payload["dispatch"].(map[string]any)
	dispatchPayload["status"], dispatchPayload["terminalAt"], dispatchPayload["message"] = status, now.Format(time.RFC3339Nano), "context maintenance settled"
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE frame_events SET payload=? WHERE id=? AND payload=?`, string(raw), row.dispatch.ResumeEvent.ID, row.rawPayload)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return transcriptstore.ErrContextCompactionStale
	}
	_, err = appendCompatibilityDispatchAuditEvent(ctx, tx, "frame-resume-dispatch-terminal:"+row.dispatch.ResumeEvent.ID, frame, "frame_resume_dispatch_"+status,
		map[string]any{"resumeEventId": row.dispatch.ResumeEvent.ID, "claimToken": row.dispatch.ClaimToken, "attempt": row.dispatch.Attempt,
			"status": status, "message": "context maintenance settled"}, now)
	return err
}
