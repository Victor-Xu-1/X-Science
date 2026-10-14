package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Store) CompleteCompatibilityFrameResumeDispatch(input CompleteCompatibilityFrameResumeDispatchInput) (FrameEvent, bool, error) {
	if s == nil || s.db == nil {
		return FrameEvent{}, false, errors.New("workspace store is closed")
	}
	input.ResumeEventID = strings.TrimSpace(input.ResumeEventID)
	input.ClaimToken = strings.TrimSpace(input.ClaimToken)
	input.Status = strings.TrimSpace(input.Status)
	if input.ResumeEventID == "" || input.ExpectedAttempt <= 0 || input.ClaimToken == "" {
		return FrameEvent{}, false, errors.New("resume event id, positive expected attempt, and claim token are required")
	}
	if input.Status != frameResumeDispatchCompleted && input.Status != frameResumeDispatchFailed && input.Status != frameResumeDispatchCancelled {
		return FrameEvent{}, false, fmt.Errorf("invalid resume dispatch terminal status %q", input.Status)
	}
	ctx := context.Background()
	var event FrameEvent
	var repeated bool
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		event, repeated, err = s.completeCompatibilityFrameResumeDispatchTx(ctx, tx, input)
		return err
	})
	return event, repeated, err
}

func (s *Store) completeCompatibilityFrameResumeDispatchTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	input CompleteCompatibilityFrameResumeDispatchInput,
) (FrameEvent, bool, error) {
	row, found, err := scanCompatibilityFrameResumeDispatch(tx.QueryRowContext(ctx, `
		SELECT e.id, e.frame_id, e.sequence, e.payload, e.created_at,
			f.root_frame_id, f.project_id, f.agent_name, f.status
		FROM frame_events e JOIN frames f ON f.id = e.frame_id
		WHERE e.id = ? AND e.event_type = 'frame_resumed'`, input.ResumeEventID))
	if err != nil {
		return FrameEvent{}, false, err
	}
	if !found {
		return FrameEvent{}, false, fmt.Errorf("resume event %q does not exist", input.ResumeEventID)
	}
	if row.dispatch.Attempt != input.ExpectedAttempt || row.dispatch.ClaimToken != input.ClaimToken {
		return FrameEvent{}, false, errors.New("resume dispatch claim is not owned by this worker")
	}
	legacyProjection, err := projectFrameResumeTerminalTx(ctx, tx, input, row.dispatch,
		isCompatibilityResumeDispatchTerminal(row.dispatch.Status))
	if err != nil {
		return FrameEvent{}, false, err
	}
	if isCompatibilityResumeDispatchTerminal(row.dispatch.Status) {
		if legacyProjection {
			if err := persistFrameTerminalStatusDescription(ctx, tx, row.dispatch.FrameID, row.dispatch.Status, row.dispatch.Error); err != nil {
				return FrameEvent{}, false, err
			}
		}
		event, found, err := compatibilityDispatchAuditEventByID(ctx, tx,
			"frame-resume-dispatch-terminal:"+input.ResumeEventID)
		if err != nil {
			return FrameEvent{}, false, err
		}
		if !found {
			return FrameEvent{}, false, errors.New("terminal resume dispatch is missing its audit event")
		}
		return event, true, nil
	}
	if row.dispatch.Status != frameResumeDispatchClaimed {
		return FrameEvent{}, false, errors.New("resume dispatch claim is not owned by this worker")
	}

	status := input.Status
	if row.dispatch.FrameStatus == "cancelled" {
		status = frameResumeDispatchCancelled
	}
	now := s.now().UTC()
	payload := row.dispatch.ResumeEvent.Payload
	dispatchPayload := payload["dispatch"].(map[string]any)
	dispatchPayload["status"] = status
	dispatchPayload["terminalAt"] = now.Format(time.RFC3339Nano)
	dispatchPayload["message"] = strings.TrimSpace(input.Message)
	if status == frameResumeDispatchFailed {
		dispatchPayload["error"] = strings.TrimSpace(input.Message)
	}
	if len(input.Details) > 0 {
		dispatchPayload["details"] = input.Details
	}
	rawUpdated, err := json.Marshal(payload)
	if err != nil {
		return FrameEvent{}, false, fmt.Errorf("encode terminal resume dispatch: %w", err)
	}
	update, err := tx.ExecContext(ctx,
		`UPDATE frame_events SET payload = ? WHERE id = ? AND payload = ?`,
		string(rawUpdated), input.ResumeEventID, row.rawPayload,
	)
	if err != nil {
		return FrameEvent{}, false, fmt.Errorf("complete resume dispatch: %w", err)
	}
	if changed, err := update.RowsAffected(); err != nil {
		return FrameEvent{}, false, err
	} else if changed != 1 {
		return FrameEvent{}, false, errors.New("resume dispatch completion changed concurrently")
	}
	if legacyProjection && row.dispatch.FrameStatus != "cancelled" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE frames SET status = ?, updated_at = ? WHERE id = ?`,
			status, now, row.dispatch.FrameID,
		); err != nil {
			return FrameEvent{}, false, fmt.Errorf("update resumed frame terminal status: %w", err)
		}
	}
	if legacyProjection {
		if err := persistFrameTerminalStatusDescription(ctx, tx, row.dispatch.FrameID, status, input.Message); err != nil {
			return FrameEvent{}, false, err
		}
	}
	eventType := "frame_resume_dispatch_" + status
	terminalEvent, err := appendCompatibilityDispatchAuditEvent(ctx, tx,
		"frame-resume-dispatch-terminal:"+input.ResumeEventID,
		row.dispatch.FrameID, eventType,
		map[string]any{
			"resumeEventId": input.ResumeEventID,
			"claimToken":    input.ClaimToken,
			"attempt":       row.dispatch.Attempt,
			"status":        status,
			"message":       strings.TrimSpace(input.Message),
			"details":       input.Details,
		}, now,
	)
	if err != nil {
		return FrameEvent{}, false, err
	}
	return terminalEvent, false, nil
}
