package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// RunnerPreparationAdmissionEventType is an internal, typed ownership fact.
// It has no model-history, tool-progress or user-message projection.
const RunnerPreparationAdmissionEventType = "runner_preparation_admitted"
const runnerUnknownAdmissionReason = "runner_execution_outcome_unknown"
const runnerExecutionAdmissionState = "execution_admitted"

// IsRunnerAdmissionFact identifies typed admission evidence for consumers that
// must advance their source cursor without manufacturing user/model activity.
func IsRunnerAdmissionFact(event Event, payload []byte) bool {
	if event.Source != EventSourcePayload || event.RunnerAttempt == nil || *event.RunnerAttempt <= 0 {
		return false
	}
	identity := event.StreamUID + ":" + strconv.FormatInt(*event.RunnerAttempt, 10)
	if event.Type == RunnerPreparationAdmissionEventType {
		return event.ClientMessageID == "runner-preparation:"+identity
	}
	if event.Type != "runner_checkpoint" || event.ClientMessageID != "runner-execution-admitted:"+identity {
		return false
	}
	var fact struct {
		DispatchState string `json:"dispatch_state"`
	}
	return json.Unmarshal(payload, &fact) == nil && fact.DispatchState == runnerExecutionAdmissionState
}

// A claim is not proof that dispatch never occurred. Only this typed admission
// plus an unchanged preparation phase proves that the fresh attempt has not
// handed off execution. Internal handoff replaces it with an execution checkpoint
// inside the same transaction, before returning authority to its caller.
const runnerPreparationProofSQL = `attempt.phase='claimed'
	AND attempt.resume_source='fresh' AND attempt.resume_checkpoint_sequence=0
	AND attempt.last_checkpoint_sequence=0
	AND NOT EXISTS (SELECT 1 FROM transcript_runner_checkpoints checkpoint
		WHERE checkpoint.stream_uid=attempt.stream_uid AND checkpoint.runner_attempt=attempt.attempt)
	AND EXISTS (SELECT 1 FROM transcript_events admission
		WHERE admission.stream_uid=attempt.stream_uid AND admission.runner_attempt=attempt.attempt
			AND admission.event_type='` + RunnerPreparationAdmissionEventType + `'
			AND admission.source='payload' AND admission.frame_event_id IS NULL
			AND admission.client_message_id='runner-preparation:'||attempt.stream_uid||':'||attempt.attempt
			AND json_extract(admission.payload_json,'$.revision')=1
			AND json_extract(admission.payload_json,'$.dispatch_state')='preparation'
			AND json_extract(admission.payload_json,'$.claimed_input_revision')=attempt.claimed_input_revision
			AND EXISTS (SELECT 1 FROM transcript_branch_state branch
				JOIN transcript_branch_events membership ON membership.stream_uid=branch.stream_uid
					AND membership.branch_id=branch.active_branch_id
				WHERE branch.stream_uid=attempt.stream_uid AND membership.event_id=admission.event_id))`

func runnerPreparationProofFor(alias string) string {
	// Only fixed query aliases within this package call this function.
	return strings.ReplaceAll(runnerPreparationProofSQL, "attempt.", alias+".")
}

func runnerPreparationIsSafeConn(ctx context.Context, conn *sql.Conn, streamUID string, attempt, inputRevision int64) (bool, error) {
	var safe bool
	err := conn.QueryRowContext(ctx, `SELECT (`+runnerPreparationProofSQL+`)
		FROM transcript_runner_attempts attempt WHERE attempt.stream_uid=? AND attempt.attempt=?
			AND attempt.claimed_input_revision=?`, streamUID, attempt, inputRevision).Scan(&safe)
	return safe, err
}

func appendRunnerPreparationAdmissionConn(ctx context.Context, conn *sql.Conn, stream Stream, claim RunnerClaim, now time.Time) error {
	if claim.ResumeSource != ResumeSourceFresh || claim.ResumeCheckpoint != 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"revision": 1, "dispatch_state": "preparation",
		"claimed_input_revision": claim.ClaimedInputRevision})
	if err != nil {
		return err
	}
	_, _, err = appendEventConn(ctx, conn, stream, eventRecord{
		clientMessageID: "runner-preparation:" + stream.UID + ":" + strconv.FormatInt(claim.Attempt, 10),
		eventType:       RunnerPreparationAdmissionEventType, source: EventSourcePayload,
		runnerAttempt: &claim.Attempt, payloadJSON: payload, createdAt: now,
	})
	return err
}

func admitInternalRunnerExecutionConn(ctx context.Context, conn *sql.Conn, claim RunnerClaim, now time.Time) error {
	payload, err := json.Marshal(map[string]any{"status": "running", "dispatch_state": runnerExecutionAdmissionState,
		"reason_code": runnerUnknownAdmissionReason, "detail": "Execution authority was handed off; replay safety is not established."})
	if err != nil {
		return err
	}
	_, _, _, err = appendRunnerCheckpointConn(ctx, conn, AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "runner-execution-admitted:" + claim.StreamUID + ":" + strconv.FormatInt(claim.Attempt, 10),
		Phase: RunnerPhaseExecuting, Resumable: false, PayloadJSON: payload,
	}, now)
	return err
}
