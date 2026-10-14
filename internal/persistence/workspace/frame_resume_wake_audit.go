package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Wake evidence describes an already eligible transition. It never grants
// execution authority or changes the dispatch's recovery policy.
type FrameResumeWakeSource string

const (
	FrameResumeWakeContinue          FrameResumeWakeSource = "continue_request"
	FrameResumeWakeKernelOperation   FrameResumeWakeSource = "kernel_operation"
	FrameResumeWakeKernelSettlement  FrameResumeWakeSource = "kernel_settlement"
	FrameResumeWakeAutomaticRecovery FrameResumeWakeSource = "automatic_recovery"
	FrameResumeWakeRecoveryCondition FrameResumeWakeSource = "recovery_condition"
	FrameResumeWakeTaskOperation     FrameResumeWakeSource = "task_operation"
	FrameResumeWakeModelSwitch       FrameResumeWakeSource = "model_switch"
	FrameResumeWakeLegacyUnknown     FrameResumeWakeSource = "legacy_unknown"
)

type FrameResumeWakeCauseKind string

type FrameResumeWakeCause struct {
	Kind                     FrameResumeWakeCauseKind `json:"kind"`
	ReferenceID              string                   `json:"referenceId,omitempty"`
	Evidence                 string                   `json:"evidence"`
	StreamUID                string                   `json:"streamUid,omitempty"`
	EventID                  int64                    `json:"eventId,omitempty"`
	RunnerAttempt            int64                    `json:"runnerAttempt,omitempty"`
	CheckpointSequence       int64                    `json:"checkpointSequence,omitempty"`
	OperationID              string                   `json:"operationId,omitempty"`
	OperationVersion         int64                    `json:"operationVersion,omitempty"`
	ExpectedOperationVersion int64                    `json:"expectedOperationVersion,omitempty"`
	DecisionID               string                   `json:"decisionId,omitempty"`
	DecisionSource           string                   `json:"decisionSource,omitempty"`
	DecisionActorID          string                   `json:"decisionActorId,omitempty"`
	NotificationID           string                   `json:"notificationId,omitempty"`
	Revision                 int64                    `json:"revision,omitempty"`
	PreviousRevision         int64                    `json:"previousRevision,omitempty"`
	RuntimeVersion           string                   `json:"runtimeVersion,omitempty"`
	RuntimeRevision          string                   `json:"runtimeRevision,omitempty"`
	RuntimeDirty             bool                     `json:"runtimeDirty,omitempty"`
}

type FrameResumeDispatchWakeInput struct {
	ResumeEventID   string
	ExpectedWaitRef string
	Source          FrameResumeWakeSource
	ActorID         string
	Cause           FrameResumeWakeCause
}

// A requeue audit identifies a single waiting period. Legacy rows use an
// opaque exact-payload snapshot, not an invented historical actor or cause.
func frameResumeDispatchWaitRef(payload map[string]any) string {
	if ref := resumeDispatchString(payload["waitEventId"]); ref != "" {
		return ref
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return "legacy-wait:" + hex.EncodeToString(digest[:])
}

func frameResumeWakeAuditID(resumeID, waitRef string) string {
	digest := sha256.Sum256([]byte(resumeID + "\x00" + waitRef))
	return "frame-resume-dispatch-woken-v2:" + hex.EncodeToString(digest[:])
}

func validateFrameResumeWake(input FrameResumeDispatchWakeInput) error {
	if strings.TrimSpace(input.ResumeEventID) == "" || strings.TrimSpace(input.ExpectedWaitRef) == "" {
		return errors.New("resume event and expected wait reference are required")
	}
	switch input.Source {
	case FrameResumeWakeContinue, FrameResumeWakeKernelOperation, FrameResumeWakeKernelSettlement,
		FrameResumeWakeAutomaticRecovery, FrameResumeWakeRecoveryCondition, FrameResumeWakeTaskOperation,
		FrameResumeWakeModelSwitch, FrameResumeWakeLegacyUnknown:
	default:
		return errors.New("resume dispatch wake source is invalid")
	}
	if input.Cause.Kind == "" {
		return errors.New("resume dispatch wake cause is required")
	}
	validKind := false
	switch input.Source {
	case FrameResumeWakeContinue:
		validKind = input.Cause.Kind == "continue_command"
	case FrameResumeWakeKernelOperation:
		validKind = input.Cause.Kind == "kernel_operation_version"
	case FrameResumeWakeKernelSettlement:
		validKind = input.Cause.Kind == "kernel_settlement_outbox"
	case FrameResumeWakeAutomaticRecovery:
		validKind = input.Cause.Kind == "auto_resume_checkpoint"
	case FrameResumeWakeRecoveryCondition:
		validKind = input.Cause.Kind == "recovery_condition_event" || input.Cause.Kind == "recovery_contract_revision"
	case FrameResumeWakeTaskOperation:
		validKind = input.Cause.Kind == "task_operation_settlement"
	case FrameResumeWakeModelSwitch:
		validKind = input.Cause.Kind == "model_selection_revision"
	case FrameResumeWakeLegacyUnknown:
		validKind = input.Cause.Kind == "legacy_unknown" && input.Cause.Evidence == "unknown" && input.ActorID == ""
	}
	if !validKind {
		return errors.New("resume wake source and cause kind do not match")
	}
	if input.Cause.Kind == "model_selection_revision" && input.Cause.Evidence != "observed" {
		return errors.New("model revision is observed evidence, not an immutable command")
	}
	switch input.Cause.Evidence {
	case "canonical", "observed":
		if input.Cause.ReferenceID == "" {
			return errors.New("resume wake evidence reference is required")
		}
	case "unknown":
		if input.Cause.ReferenceID != "" {
			return errors.New("unknown resume wake cause cannot claim a reference")
		}
	default:
		return errors.New("resume dispatch wake evidence status is invalid")
	}
	if input.Source == FrameResumeWakeContinue && (input.ActorID == "" || input.Cause.Kind != "continue_command" || input.Cause.ReferenceID == "") {
		return errors.New("continue wake requires its authenticated actor and command identity")
	}
	return nil
}

func appendFrameResumeWakeAudit(ctx context.Context, tx workspaceTransaction, row compatibilityFrameResumeDispatchRow, input FrameResumeDispatchWakeInput, now time.Time) (FrameEvent, error) {
	waitOrigin, err := frameResumeWaitOrigin(ctx, tx, row)
	if err != nil {
		return FrameEvent{}, err
	}
	cause := input.Cause
	if input.Source == FrameResumeWakeContinue {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT p.user_id FROM frames f JOIN projects p ON p.id=f.project_id WHERE f.id=?`, row.dispatch.FrameID).Scan(&owner); err != nil {
			return FrameEvent{}, err
		}
		if owner != input.ActorID {
			return FrameEvent{}, errors.New("continue wake actor does not own the frame")
		}
		// This receipt proves an accepted authenticated request, not a physical
		// human click, and does not supply cross-wait HTTP idempotency.
		command, err := appendCompatibilityDispatchAuditEvent(ctx, tx,
			"frame-resume-continue-command:"+cause.ReferenceID, row.dispatch.FrameID,
			"frame_resume_continue_command_accepted", map[string]any{
				"resumeEventId": input.ResumeEventID, "waitRef": input.ExpectedWaitRef,
				"actorId": input.ActorID, "source": string(input.Source),
			}, now)
		if err != nil {
			return FrameEvent{}, err
		}
		if resumeDispatchString(command.Payload["waitRef"]) != input.ExpectedWaitRef || resumeDispatchString(command.Payload["actorId"]) != input.ActorID {
			return FrameEvent{}, errors.New("continue command identity conflicts with the accepted wait")
		}
		cause.ReferenceID, cause.Evidence = command.ID, "canonical"
	}
	return appendCompatibilityDispatchAuditEvent(ctx, tx,
		frameResumeWakeAuditID(input.ResumeEventID, input.ExpectedWaitRef), row.dispatch.FrameID,
		"frame_resume_dispatch_woken", map[string]any{
			"resumeEventId": input.ResumeEventID, "dispatchAttempt": row.dispatch.Attempt,
			"priorStatus": row.dispatch.Status, "waitRef": input.ExpectedWaitRef,
			"source": string(input.Source), "actorId": input.ActorID, "cause": cause,
			"waitOriginEventId": waitOrigin,
		}, now)
}

// Older rows did not persist a wait ID. Link an existing interruption only
// when its exact generation and recorded coordinates match; otherwise the
// opaque snapshot remains the sole identity and historical cause is unknown.
func frameResumeWaitOrigin(ctx context.Context, tx workspaceTransaction, row compatibilityFrameResumeDispatchRow) (string, error) {
	if !strings.HasPrefix(row.dispatch.WaitRef, "legacy-wait:") {
		return row.dispatch.WaitRef, nil
	}
	if row.dispatch.Attempt <= 0 {
		return "", nil
	}
	id := fmt.Sprintf("frame-resume-dispatch-interrupted:%s:%d", row.dispatch.ResumeEvent.ID, row.dispatch.Attempt)
	event, found, err := compatibilityDispatchAuditEventByID(ctx, tx, id)
	if err != nil || !found {
		return "", err
	}
	payload, _ := row.dispatch.ResumeEvent.Payload["dispatch"].(map[string]any)
	if event.FrameID != row.dispatch.FrameID || event.Type != "frame_resume_dispatch_interrupted" ||
		resumeDispatchInt(event.Payload["dispatchAttempt"]) != row.dispatch.Attempt ||
		resumeDispatchInt(event.Payload["runnerAttempt"]) != resumeDispatchInt(payload["runnerAttempt"]) ||
		resumeDispatchInt(event.Payload["checkpointEventId"]) != resumeDispatchInt(payload["checkpointEventId"]) ||
		resumeDispatchString(event.Payload["reasonCode"]) != resumeDispatchString(payload["interruptionReasonCode"]) ||
		!event.CreatedAt.Equal(resumeDispatchTime(payload["interruptedAt"])) {
		return "", nil
	}
	return id, nil
}
