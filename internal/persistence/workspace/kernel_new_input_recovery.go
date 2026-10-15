package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Reconcile only never-prepared work at the existing live-claim recovery
// admission boundary. The transaction retains original inputs, materializes a
// non-execution result, and leaves normal protocol recovery to publish it once.
func (s *Store) supersedeUnstartedKernelOperationsForInputTx(
	ctx context.Context, tx *transcriptstore.ImmediateTransaction,
	stream transcriptstore.Stream, claim transcriptstore.RunnerClaim,
) (bool, error) {
	if stream.InputRevision != claim.ClaimedInputRevision {
		return false, transcriptstore.ErrClaimStale
	}
	input, err := latestKernelRecoveryComposerInput(ctx, tx, stream)
	if err != nil || input.eventID == 0 {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.operation_id FROM kernel_local_operations o
		JOIN transcript_tool_call_batches b ON b.stream_uid=o.stream_uid AND b.source_event_id=o.source_event_id
		JOIN transcript_tool_call_items item ON item.batch_id=b.batch_id AND item.ordinal=o.tool_call_ordinal
			AND item.tool_call_id=o.tool_call_id AND item.state IN ('pending','running','waiting')
		JOIN transcript_branch_state branch ON branch.stream_uid=o.stream_uid
		JOIN transcript_branch_events source ON source.stream_uid=o.stream_uid AND source.branch_id=o.branch_id AND source.event_id=o.source_event_id
		JOIN transcript_runner_attempts attempt ON attempt.stream_uid=o.stream_uid AND attempt.attempt=o.source_runner_attempt
		WHERE o.owner_user_id=? AND o.stream_uid=? AND o.branch_id=branch.active_branch_id AND o.branch_generation=branch.generation
		AND b.state IN ('ready','running','waiting') AND o.source_publication_seq<?
		AND o.state IN ('pending_approval','approved') AND o.prepared_at IS NULL AND o.started_at IS NULL AND o.execution_id IS NULL
		AND COALESCE(o.admitted_input_revision,attempt.claimed_input_revision)<?
		ORDER BY o.source_publication_seq,o.tool_call_ordinal,o.operation_id`, claim.OwnerID, claim.StreamUID, input.publication, claim.ClaimedInputRevision)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	changed := false
	for _, id := range ids {
		operation, found, err := getKernelLocalOperationQuery(ctx, tx, id)
		if err != nil || !found {
			if err == nil {
				err = ErrKernelLocalOperationConflict
			}
			return false, err
		}
		precedes, err := kernelRecoveryApprovalPrecedesInput(ctx, tx, operation, input)
		if err != nil {
			return false, err
		}
		if !precedes {
			continue
		}
		result, err := kernelRecoverySupersessionFeedback(input, claim.ClaimedInputRevision)
		if err != nil {
			return false, err
		}
		if _, err := s.completeKernelLocalOperationPreflightTx(ctx, tx, CompleteKernelLocalOperationPreflightInput{
			OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedState: operation.State,
			ExpectedStateVersion: operation.StateVersion, Claim: claim, ReasonCode: "superseded_by_new_user_input", TerminalResultJSON: result,
		}, &input); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func kernelRecoverySupersessionFeedback(input kernelRecoveryInputAuthority, recoveryRevision int64) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"ok": true, "executed": false, "preflight": true, "status": "superseded_by_new_user_input", "execution_outcome": "not_started",
		"message":                    "A newer user message superseded this unstarted operation. No code was executed. Interpret the newer input before choosing further tools.",
		"superseding_input_event_id": input.eventID, "recovery_input_revision": recoveryRevision,
	})
}

type kernelRecoveryInputAuthority struct {
	eventID, publication, frameSequence int64
}

func latestKernelRecoveryComposerInput(ctx context.Context, tx *transcriptstore.ImmediateTransaction, stream transcriptstore.Stream) (kernelRecoveryInputAuthority, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.event_id,e.publication_seq,e.event_type,e.client_message_id,COALESCE(f.sequence,0),
		CASE WHEN e.source='payload' THEN e.payload_json ELSE f.payload END
		FROM transcript_events e JOIN transcript_branch_state b ON b.stream_uid=e.stream_uid
		JOIN transcript_branch_events m ON m.stream_uid=e.stream_uid AND m.event_id=e.event_id AND m.branch_id=b.active_branch_id
		LEFT JOIN frame_events f ON e.source='frame_ref' AND f.id=e.frame_event_id AND f.frame_id=? AND f.event_type=e.event_type
		WHERE e.stream_uid=? AND e.event_type IN ('user_message','user_input_response') AND e.source IN ('payload','frame_ref')
		ORDER BY m.ordinal DESC`, stream.FrameID, stream.UID)
	if err != nil {
		return kernelRecoveryInputAuthority{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var input kernelRecoveryInputAuthority
		var eventType, clientID string
		var payload []byte
		if err := rows.Scan(&input.eventID, &input.publication, &eventType, &clientID, &input.frameSequence, &payload); err != nil {
			return kernelRecoveryInputAuthority{}, err
		}
		if transcriptstore.IsCanonicalFrameUserMessage(eventType, clientID, payload) {
			return input, rows.Close()
		}
	}
	return kernelRecoveryInputAuthority{}, rows.Err()
}

// Approval and input ordering must use durable sequence authorities, not wall
// clocks. A later explicit approval remains authoritative even if another
// AskUser response has since advanced the runner's input revision again.
func kernelRecoveryApprovalPrecedesInput(ctx context.Context, tx *transcriptstore.ImmediateTransaction, operation KernelLocalOperation, input kernelRecoveryInputAuthority) (bool, error) {
	if operation.State == KernelLocalOperationStatePendingApproval {
		return true, nil
	}
	var approvalSequence int64
	err := tx.QueryRowContext(ctx, `SELECT sequence FROM frame_events WHERE id=? AND frame_id=? AND event_type=?
		AND json_extract(payload,'$.operation_id')=? AND json_extract(payload,'$.decision_id')=? AND json_extract(payload,'$.decision')='allow'`,
		kernelLocalOperationApprovalResolvedEventID(operation.OperationID, operation.ApprovalDecisionID), operation.FrameID,
		KernelLocalExecApprovalResolvedEventType, operation.OperationID, operation.ApprovalDecisionID).Scan(&approvalSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // Unknown approval provenance is not permission to retire work.
	}
	if err != nil || input.frameSequence > 0 {
		return err == nil && approvalSequence < input.frameSequence, err
	}
	var approvalPublication int64
	err = tx.QueryRowContext(ctx, `SELECT e.publication_seq FROM transcript_events e
		JOIN transcript_branch_events member ON member.stream_uid=e.stream_uid AND member.event_id=e.event_id AND member.branch_id=?
		WHERE e.stream_uid=? AND e.client_message_id=? AND e.event_type='user_input_response' AND e.source='payload'
		AND json_extract(e.payload_json,'$.kind')='kernel_local_exec_approval'
		AND json_extract(e.payload_json,'$.operation_id')=? AND json_extract(e.payload_json,'$.decision_id')=?
		AND json_extract(e.payload_json,'$.decision')='allow'`, operation.BranchID, operation.StreamUID,
		"kernel-approval-response:"+operation.OperationID+":"+operation.ApprovalDecisionID, operation.OperationID, operation.ApprovalDecisionID).Scan(&approvalPublication)
	if err == nil {
		return approvalPublication < input.publication, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// A policy decision admitted against the original model attempt still owns
	// that older input. A decision from any later/unknown revision is preserved.
	var sourceRevision int64
	err = tx.QueryRowContext(ctx, `SELECT claimed_input_revision FROM transcript_runner_attempts WHERE stream_uid=? AND attempt=?`,
		operation.StreamUID, operation.SourceRunnerAttempt).Scan(&sourceRevision)
	return err == nil && operation.ApprovalSource == "policy" && operation.AdmittedInputRevision == sourceRevision, err
}
