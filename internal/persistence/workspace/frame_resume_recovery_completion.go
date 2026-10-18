package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

type recoveryCompletionScope struct {
	row                compatibilityFrameResumeDispatchRow
	ownerID            string
	frameIncarnationID string
	rootIncarnationID  string
	streamUID          string
	branchID           string
	branchGeneration   int64
	inputRevision      int64
	claimSequence      int64
}

// WakeRecoveryConditionAfterKernelCompletion joins a parked wait to an unread
// completion committed after its exact dispatch claim. The next claim's audit
// sequence excludes that completion even if its notification remains unread.
// This does not acknowledge notifications or authorize replay of the operation.
func (s *Store) WakeRecoveryConditionAfterKernelCompletion(ctx context.Context, resumeEventID, expectedWaitRef string) (FrameEvent, bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return FrameEvent{}, false, errors.New("workspace store and context are required")
	}
	resumeEventID, expectedWaitRef = strings.TrimSpace(resumeEventID), strings.TrimSpace(expectedWaitRef)
	if resumeEventID == "" || expectedWaitRef == "" {
		return FrameEvent{}, false, errors.New("resume event and expected wait reference are required")
	}
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return FrameEvent{}, false, err
	}
	var event FrameEvent
	var changed bool
	err = repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		scope, eligible, err := recoveryCompletionScopeTx(ctx, tx, resumeEventID, expectedWaitRef)
		if err != nil || !eligible {
			return err
		}
		operation, notificationID, found, err := recoveryCompletionOperationTx(ctx, tx, scope)
		if err != nil || !found {
			return err
		}
		event, changed, err = wakeCompatibilityFrameResumeDispatchTx(ctx, tx, FrameResumeDispatchWakeInput{
			ResumeEventID: resumeEventID, ExpectedWaitRef: expectedWaitRef,
			Source: FrameResumeWakeKernelOperation,
			Cause: FrameResumeWakeCause{
				Kind: "kernel_operation_version", Evidence: "observed", ReferenceID: operation.OperationID,
				OperationID: operation.OperationID, OperationVersion: operation.StateVersion,
				NotificationID: notificationID,
			},
		}, s.now().UTC())
		return err
	})
	if err != nil {
		return FrameEvent{}, false, fmt.Errorf("join recovery wait to kernel completion: %w", err)
	}
	return event, changed, nil
}

func recoveryCompletionScopeTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, resumeID, waitRef string) (recoveryCompletionScope, bool, error) {
	row, found, err := scanCompatibilityFrameResumeDispatch(tx.QueryRowContext(ctx, `
		SELECT e.id,e.frame_id,e.sequence,e.payload,e.created_at,
			f.root_frame_id,f.project_id,f.agent_name,f.status
		FROM frame_events e JOIN frames f ON f.id=e.frame_id
		WHERE e.id=? AND e.event_type='frame_resumed'
			AND NOT EXISTS (SELECT 1 FROM frame_events later
				WHERE later.frame_id=e.frame_id AND later.event_type='frame_resumed'
					AND json_extract(later.payload,'$.dispatch.status') IS NOT NULL
					AND later.sequence>e.sequence)`, resumeID))
	if err != nil || !found {
		return recoveryCompletionScope{}, false, err
	}
	if row.dispatch.Status != frameResumeDispatchRegistered ||
		row.dispatch.WaitingFor != CompatibilityFrameResumeDispatchWaitRecoveryCondition ||
		row.dispatch.WaitRef != waitRef || row.dispatch.Attempt <= 0 {
		return recoveryCompletionScope{}, false, nil
	}
	if err := transcriptstore.CheckFrameConversationAvailable(ctx, tx, row.dispatch.FrameID); err != nil {
		if errors.Is(err, transcriptstore.ErrConversationArchived) {
			return recoveryCompletionScope{}, false, nil
		}
		return recoveryCompletionScope{}, false, err
	}
	scope := recoveryCompletionScope{row: row}
	if err := tx.QueryRowContext(ctx, `SELECT project.user_id,frame.incarnation_id,root.incarnation_id
		FROM frames frame JOIN projects project ON project.id=frame.project_id
		JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
		WHERE frame.id=? AND frame.project_id=? AND frame.root_frame_id=?
			AND frame.status IN ('processing','running')
			AND LOWER(TRIM(root.status)) NOT IN ('completed','failed','cancelled','canceled')`,
		row.dispatch.FrameID, row.dispatch.ProjectID, row.dispatch.RootFrameID,
	).Scan(&scope.ownerID, &scope.frameIncarnationID, &scope.rootIncarnationID); err != nil {
		return scope, false, recoveryCompletionMissingIsNoop(err)
	}
	claimSequence, err := recoveryCompletionClaimSequenceTx(ctx, tx, row)
	if err != nil || claimSequence <= 0 {
		return scope, false, err
	}
	scope.claimSequence = claimSequence
	authority, found, err := tx.GetFrameAuthorityBySession(ctx, scope.ownerID, row.dispatch.FrameID)
	if err != nil || !found {
		return scope, false, err
	}
	scope.streamUID = authority.ActiveStreamUID
	eligible, err := recoveryCompletionInputScopeTx(ctx, tx, &scope, authority.ActiveEpoch)
	return scope, eligible, err
}

func recoveryCompletionClaimSequenceTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, row compatibilityFrameResumeDispatchRow) (int64, error) {
	dispatch := row.dispatch
	interruptionID := fmt.Sprintf("frame-resume-dispatch-interrupted:%s:%d", dispatch.ResumeEvent.ID, dispatch.Attempt)
	if dispatch.WaitRef != interruptionID {
		return 0, nil // Legacy waits have no proven claim-start observation boundary.
	}
	interruption, found, err := compatibilityDispatchAuditEventByID(ctx, tx, interruptionID)
	if err != nil || !found {
		return 0, err
	}
	payload := dispatch.ResumeEvent.Payload["dispatch"].(map[string]any)
	if interruption.Type != "frame_resume_dispatch_interrupted" || interruption.FrameID != dispatch.FrameID ||
		resumeDispatchInt(interruption.Payload["dispatchAttempt"]) != dispatch.Attempt ||
		resumeDispatchInt(interruption.Payload["runnerAttempt"]) != resumeDispatchInt(payload["runnerAttempt"]) ||
		resumeDispatchInt(interruption.Payload["checkpointEventId"]) != resumeDispatchInt(payload["checkpointEventId"]) ||
		resumeDispatchString(interruption.Payload["reasonCode"]) != resumeDispatchString(payload["interruptionReasonCode"]) {
		return 0, nil
	}
	claim, found, err := compatibilityDispatchAuditEventByID(ctx, tx,
		fmt.Sprintf("frame-resume-dispatch-claim:%s:%d", dispatch.ResumeEvent.ID, dispatch.Attempt))
	if err != nil || !found {
		return 0, err
	}
	token := resumeDispatchString(claim.Payload["claimToken"])
	if claim.Type != "frame_resume_dispatch_claimed" || claim.FrameID != dispatch.FrameID ||
		claim.Sequence <= 0 || claim.Sequence >= interruption.Sequence || token == "" ||
		resumeDispatchString(claim.Payload["resumeEventId"]) != dispatch.ResumeEvent.ID ||
		resumeDispatchInt(claim.Payload["attempt"]) != dispatch.Attempt ||
		resumeDispatchString(claim.Payload["claimOwner"]) == "" ||
		kernelLocalOperationClaimSHA256(token) != resumeDispatchString(payload["interruptedClaimTokenSHA256"]) {
		return 0, nil
	}
	return claim.Sequence, nil
}

func recoveryCompletionInputScopeTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, scope *recoveryCompletionScope, epoch int64) (bool, error) {
	dispatch := scope.row.dispatch
	payload := dispatch.ResumeEvent.Payload["dispatch"].(map[string]any)
	runnerAttempt := resumeDispatchInt(payload["runnerAttempt"])
	checkpointID := resumeDispatchInt(payload["checkpointEventId"])
	if runnerAttempt <= 0 || checkpointID <= 0 || epoch <= 0 {
		return false, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT stream.input_revision,branch.active_branch_id,branch.generation
		FROM transcript_streams stream
		JOIN transcript_branch_state branch ON branch.stream_uid=stream.stream_uid
		JOIN transcript_runner_attempts attempt ON attempt.stream_uid=stream.stream_uid
		JOIN transcript_runner_checkpoints checkpoint ON checkpoint.stream_uid=attempt.stream_uid
			AND checkpoint.runner_attempt=attempt.attempt AND checkpoint.checkpoint_sequence=attempt.last_checkpoint_sequence
		JOIN transcript_events event ON event.stream_uid=checkpoint.stream_uid AND event.event_id=checkpoint.event_id
			AND event.runner_attempt=attempt.attempt
		JOIN transcript_branch_events member ON member.stream_uid=event.stream_uid
			AND member.branch_id=branch.active_branch_id AND member.event_id=event.event_id
		WHERE stream.stream_uid=? AND stream.owner_id=? AND stream.epoch=? AND stream.kind='frame_ref'
			AND stream.session_id=? AND stream.frame_id=? AND stream.root_frame_id=? AND stream.project_id=?
			AND stream.input_revision>0 AND stream.input_revision=attempt.claimed_input_revision
			AND attempt.attempt=? AND attempt.status='running'
			AND attempt.attempt=(SELECT MAX(latest.attempt) FROM transcript_runner_attempts latest WHERE latest.stream_uid=stream.stream_uid)
			AND checkpoint.event_id=? AND checkpoint.resumable=1
			AND event.event_type='runner_checkpoint' AND event.source='payload'
			AND json_extract(event.payload_json,'$.status')='interrupted'
			AND json_extract(event.payload_json,'$.auto_resume')=0
			AND json_extract(event.payload_json,'$.reason_code')=?`,
		scope.streamUID, scope.ownerID, epoch, dispatch.FrameID, dispatch.FrameID, dispatch.RootFrameID,
		dispatch.ProjectID, runnerAttempt, checkpointID, resumeDispatchString(payload["interruptionReasonCode"]),
	).Scan(&scope.inputRevision, &scope.branchID, &scope.branchGeneration)
	if err != nil {
		return false, recoveryCompletionMissingIsNoop(err)
	}
	return scope.branchID != "" && scope.branchGeneration > 0, nil
}

func recoveryCompletionOperationTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, scope recoveryCompletionScope) (KernelLocalOperation, string, bool, error) {
	var operationID, notificationID string
	err := tx.QueryRowContext(ctx, `SELECT operation.operation_id,notification.id
		FROM frame_events terminal
		JOIN kernel_local_operations operation ON operation.operation_id=json_extract(terminal.payload,'$.operation_id')
		JOIN kernel_local_operation_materializations material ON material.operation_id=operation.operation_id
			AND material.execution_log_sha256=operation.result_sha256
		JOIN notifications notification ON notification.sender_frame_id=operation.frame_id
			AND notification.recipient_frame_id=operation.frame_id AND notification.root_frame_id=operation.root_frame_id
			AND notification.notification_type='cell_result' AND notification.read_at IS NULL
			AND json_extract(notification.payload_json,'$.exec_id')=operation.execution_id
		JOIN transcript_branch_events source ON source.stream_uid=operation.stream_uid
			AND source.branch_id=operation.branch_id AND source.event_id=operation.source_event_id
		WHERE terminal.frame_id=? AND terminal.event_type='kernel_local_operation_terminal' AND terminal.sequence>?
			AND terminal.id='kernel-local-operation-terminal:' || operation.operation_id
			AND json_extract(terminal.payload,'$.state')=operation.state
			AND json_extract(terminal.payload,'$.state_version')=operation.state_version
			AND json_extract(terminal.payload,'$.execution_log_id')=operation.execution_log_id
			AND json_extract(terminal.payload,'$.execution_log_sha256')=operation.result_sha256
			AND operation.owner_user_id=? AND operation.project_id=? AND operation.root_frame_id=?
			AND operation.frame_id=? AND operation.frame_incarnation_id=? AND operation.root_frame_incarnation_id=?
			AND operation.stream_uid=? AND operation.branch_id=? AND operation.branch_generation=?
			AND operation.admitted_input_revision=? AND operation.execution_id<>'' AND operation.execution_log_id<>''
			AND operation.state IN ('completed','failed','cancelled')
		ORDER BY terminal.sequence,operation.operation_id,notification.sequence LIMIT 1`,
		scope.row.dispatch.FrameID, scope.claimSequence, scope.ownerID, scope.row.dispatch.ProjectID,
		scope.row.dispatch.RootFrameID, scope.row.dispatch.FrameID, scope.frameIncarnationID, scope.rootIncarnationID,
		scope.streamUID, scope.branchID, scope.branchGeneration, scope.inputRevision,
	).Scan(&operationID, &notificationID)
	if err != nil {
		return KernelLocalOperation{}, "", false, recoveryCompletionMissingIsNoop(err)
	}
	operation, found, err := getKernelLocalOperationQuery(ctx, tx, operationID)
	if err != nil || !found {
		return operation, "", false, err
	}
	if err := validateKernelLocalOperationCurrentAuthority(ctx, tx, operation, true); err != nil {
		return operation, "", false, err
	}
	material, found, err := getKernelToolResultMaterializationQuery(ctx, tx, operationID)
	if err != nil || !found {
		return operation, "", false, err
	}
	if material.ExecutionLogSHA256 != operation.ResultSHA256 {
		return operation, "", false, ErrKernelLocalOperationConflict
	}
	return operation, notificationID, true, nil
}

func recoveryCompletionMissingIsNoop(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
