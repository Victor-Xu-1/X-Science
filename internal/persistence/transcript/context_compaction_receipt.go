package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func contextCompactionReceiptConn(ctx context.Context, conn *sql.Conn, owner, frame string, request ContextCompactionRequest, now time.Time) (ContextCompactionReceipt, error) {
	result := ContextCompactionReceipt{Schema: "x-science.context-compaction-receipt.v1", Request: request, FrameID: frame,
		State: "accepted", CancellationStatus: "not_requested", ExecutionObservation: "unavailable"}
	target, currentStream, frameStatus, err := contextCompactionTargetConn(ctx, conn, owner, frame)
	if err != nil {
		return result, err
	}
	if !sameContextCompactionIdentity(target, request.Expected) {
		return result, ErrContextCompactionStale
	}
	intent := request.MessageIntent(owner, frame)
	if cancelled, found, err := ReadFrameMessageIntentCancellation(ctx, conn, intent); err != nil {
		return result, err
	} else if found && !cancelled.Accepted {
		result.State, result.CancellationStatus, result.ExecutionObservation = "cancelled", cancelled.CancellationStatus, "not_started"
		availability, err := contextCompactionAvailabilityConn(ctx, conn, currentStream, frameStatus)
		if err != nil {
			return result, err
		}
		result.CanSubmitNextInput = availability.Available
		return result, nil
	}
	accepted, found, err := ReadAcceptedFrameMessageSnapshot(ctx, conn, intent)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrContextCompactionNotFound
	}
	var payload map[string]any
	if err = json.Unmarshal(accepted.PayloadJSON, &payload); err != nil {
		return result, err
	}
	stored, err := ContextCompactionRequestFromPayload(payload)
	if err != nil || stored == nil || *stored != request {
		return result, ErrContextCompactionConflict
	}
	result.Accepted, result.InputMessageID, result.InputRevision = true, intent.InputMessageID, accepted.InputRevision
	var status string
	var expires time.Time
	var terminal sql.NullInt64
	err = conn.QueryRowContext(ctx, `SELECT attempt,status,expires_at,finished_event_id FROM transcript_runner_attempts
		WHERE stream_uid=? AND claimed_input_revision=? ORDER BY attempt DESC LIMIT 1`, target.StreamUID, accepted.InputRevision).
		Scan(&result.Attempt, &status, &expires, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		if cancelled, found, readErr := ReadFrameMessageIntentCancellation(ctx, conn, intent); readErr != nil {
			return result, readErr
		} else if found {
			result.State, result.CancellationStatus, result.ExecutionObservation = "cancelled", cancelled.CancellationStatus, "not_started"
			availability, err := contextCompactionAvailabilityConn(ctx, conn, currentStream, frameStatus)
			if err != nil {
				return result, err
			}
			result.CanSubmitNextInput = availability.Available
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if cancelled, found, readErr := ReadFrameMessageIntentCancellation(ctx, conn, intent); readErr != nil {
		return result, readErr
	} else if found {
		result.State, result.CancellationStatus = "cancelled", cancelled.CancellationStatus
		if terminal.Valid {
			result.TerminalEventID = terminal.Int64
		}
		availability, err := contextCompactionAvailabilityConn(ctx, conn, currentStream, frameStatus)
		if err != nil {
			return result, err
		}
		result.CanSubmitNextInput = availability.Available
		return result, nil
	}
	if status == "running" {
		if !expires.After(now) {
			result.State = "recovery_needed"
		}
		return result, nil
	}
	if !terminal.Valid {
		result.State = "recovery_needed"
		return result, nil
	}
	result.TerminalEventID = terminal.Int64
	result.State = status
	if status == "reclaimed" {
		result.State = "recovery_needed"
		return result, nil
	}
	if status == "completed" {
		result.State = "committed"
	}
	if status == "cancelled" {
		result.CancellationStatus = "cancelled"
	}
	var boundary int64
	err = conn.QueryRowContext(ctx, `SELECT event_id FROM transcript_events WHERE stream_uid=? AND client_message_id=?`, target.StreamUID, "context-compaction-boundary:"+request.RequestID).Scan(&boundary)
	if err == nil {
		result.BoundaryEventID = boundary
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if result.State == "committed" && result.BoundaryEventID == 0 {
		return result, ErrEventConflict
	}
	if result.State == "committed" {
		var archived bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM compaction_archives WHERE frame_id=? AND source_event_id=?)`, frame, result.BoundaryEventID).Scan(&archived); err != nil {
			return result, err
		}
		result.ArchiveStatus = "pending"
		if archived {
			result.ArchiveStatus = "available"
		}
	}
	availability, err := contextCompactionAvailabilityConn(ctx, conn, currentStream, frameStatus)
	if err != nil {
		return result, err
	}
	result.CanSubmitNextInput = availability.Available
	return result, nil
}
