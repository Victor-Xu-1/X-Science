package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Archive is not cancellation. Every durable descendant, including hidden
// Aside children, must be quiescent before the root can leave active lists.
func (s *Store) assertConversationArchiveIdleTx(ctx context.Context, tx workspaceTransaction, rootID string, now time.Time) error {
	checks := []string{
		`SELECT EXISTS(SELECT 1 FROM frames WHERE root_frame_id=? AND status IN ('awaiting_user_response','awaiting_plan_approval'))`,
		`SELECT EXISTS(SELECT 1 FROM queued_user_messages q JOIN frames f ON f.id=q.frame_id
			WHERE f.root_frame_id=? AND q.state IN ('queued','delivering'))`,
		`SELECT EXISTS(SELECT 1 FROM frame_events e JOIN frames f ON f.id=e.frame_id
			WHERE f.root_frame_id=? AND e.event_type='frame_resumed'
			AND json_extract(e.payload,'$.dispatch.status') IN ('registered','claimed','blocked'))`,
		`SELECT EXISTS(SELECT 1 FROM kernel_local_operations WHERE root_frame_id=?
			AND state IN ('pending_approval','approved','prepared','started','outcome_unknown'))`,
		`SELECT EXISTS(SELECT 1 FROM kernel_detached_executions e JOIN kernel_local_operations o ON o.operation_id=e.operation_id
			WHERE o.root_frame_id=? AND e.state<>'terminal')`,
		`SELECT EXISTS(SELECT 1 FROM scientific_compute_admissions a LEFT JOIN scientific_compute_submissions s ON s.job_id=a.job_id
			WHERE a.root_frame_id=? AND (a.state='admitted' OR s.state IN ('prepared','creating','bound','submitted')))`,
		`SELECT EXISTS(SELECT 1 FROM compute_workbench_jobs j JOIN frames f ON f.id=COALESCE(NULLIF(j.frame_id,''),j.root_frame_id)
			JOIN projects p ON p.id=f.project_id AND p.id=j.project_id AND p.user_id=j.owner_user_id
			WHERE f.root_frame_id=? AND j.state IN ('pending','staging','queued','running','harvesting'))`,
		`SELECT EXISTS(SELECT 1 FROM transcript_tool_call_batches b JOIN transcript_streams s ON s.stream_uid=b.stream_uid
			WHERE s.root_frame_id=? AND b.state IN ('ready','running','waiting','outcome_unknown'))`,
	}
	for _, query := range checks {
		var busy bool
		if err := tx.QueryRowContext(ctx, query, rootID).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrConversationArchiveBusy
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT f.status,COALESCE(authority.active_stream_uid,''),COALESCE(s.stream_uid,''),COALESCE(s.input_revision,0),
		COALESCE(s.consumed_input_revision,0),COALESCE(a.status,''),COALESCE(a.claimed_input_revision,0),
		a.expires_at,COALESCE(a.phase,''),COALESCE((SELECT e.payload_json FROM transcript_runner_checkpoints c
			JOIN transcript_events e ON e.stream_uid=c.stream_uid AND e.event_id=c.event_id
				WHERE c.stream_uid=s.stream_uid AND c.runner_attempt=a.attempt ORDER BY c.checkpoint_sequence DESC LIMIT 1),''),
		EXISTS(SELECT 1 FROM transcript_runner_receipts receipt
			JOIN transcript_events terminal ON terminal.stream_uid=receipt.stream_uid AND terminal.event_id=receipt.event_id
			WHERE receipt.stream_uid=a.stream_uid AND receipt.attempt=a.attempt AND receipt.event_id=a.finished_event_id
				AND receipt.status='completed' AND terminal.event_type='runner_finished' AND terminal.runner_attempt=a.attempt)
		FROM frames f JOIN projects p ON p.id=f.project_id
		LEFT JOIN transcript_frame_authority authority ON authority.owner_id=p.user_id AND authority.session_id=f.id
		LEFT JOIN transcript_streams s ON s.stream_uid=authority.active_stream_uid AND s.epoch=authority.active_epoch
			AND s.owner_id=p.user_id AND s.frame_id=f.id AND s.kind='frame_ref'
		LEFT JOIN transcript_runner_attempts a ON a.stream_uid=s.stream_uid
			AND a.attempt=(SELECT MAX(latest.attempt) FROM transcript_runner_attempts latest WHERE latest.stream_uid=s.stream_uid)
		WHERE f.root_frame_id=?`, rootID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var status, authorityStream, stream, attemptStatus, phase, checkpoint string
		var input, consumed, claimed int64
		var expiresAt *time.Time
		var terminalReceipt bool
		if err := rows.Scan(&status, &authorityStream, &stream, &input, &consumed, &attemptStatus, &claimed, &expiresAt, &phase, &checkpoint, &terminalReceipt); err != nil {
			return err
		}
		if authorityStream != "" && stream == "" {
			return ErrConversationArchiveBusy
		}
		paused := attemptStatus == "running" && expiresAt != nil && !expiresAt.After(now) && claimed == input &&
			archiveCheckpointIsExplicitlyPaused(checkpoint)
		if attemptStatus == "running" && !paused || phase == "waiting_user" || phase == "waiting_approval" || phase == "waiting_external" {
			return ErrConversationArchiveBusy
		}
		if consumed > input || claimed > input || input > consumed && !paused {
			return ErrConversationArchiveBusy
		}
		completed := attemptStatus == "completed" && phase == "terminal" && terminalReceipt && input == consumed
		if status == FrameStatusProcessing && !paused && !completed {
			// A stale processing label alone cannot prove a pause; nor may it
			// override a matching pause or a canonical finished/consumed prefix.
			return ErrConversationArchiveBusy
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

func archiveCheckpointIsExplicitlyPaused(raw string) bool {
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil || payload["status"] != "interrupted" {
		return false
	}
	automatic, valid := payload["auto_resume"].(bool)
	return valid && !automatic
}
