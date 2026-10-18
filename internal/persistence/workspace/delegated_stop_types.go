package workspace

import (
	"errors"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	DelegatedStopReceiptEventType = "delegated_stop_receipt"
	delegationAdmissionEventType  = "delegation_admission"
	delegationOriginEventType     = "delegation_origin"
	delegationFenceEventType      = "delegation_branch_fence"
)

var (
	ErrDelegatedStopInvalid           = errors.New("DELEGATED_STOP_INVALID")
	ErrDelegatedStopUnavailable       = errors.New("DELEGATED_STOP_UNAVAILABLE")
	ErrDelegatedStopScopeStale        = errors.New("DELEGATED_STOP_SCOPE_STALE")
	ErrDelegatedStopBranchStale       = errors.New("DELEGATED_STOP_BRANCH_STALE")
	ErrDelegatedStopTargetStale       = errors.New("DELEGATED_STOP_TARGET_STALE")
	ErrDelegatedStopRequestConflict   = errors.New("DELEGATED_STOP_REQUEST_CONFLICT")
	ErrDelegatedStopOriginUnavailable = errors.New("DELEGATED_STOP_ORIGIN_UNAVAILABLE")
)

type DelegatedStopScope struct {
	ParentFrameID            string `json:"parent_frame_id"`
	ParentFrameIncarnationID string `json:"parent_frame_incarnation_id"`
	RootFrameID              string `json:"root_frame_id"`
	RootFrameIncarnationID   string `json:"root_frame_incarnation_id"`
}

type DelegatedStopBranch struct {
	BranchID   string `json:"branch_id"`
	Generation int64  `json:"generation"`
}

type DelegatedStopTarget struct {
	FrameID            string `json:"frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	StreamUID          string `json:"stream_uid"`
	StreamEpoch        int64  `json:"stream_epoch"`
	InputMessageID     string `json:"input_message_id"`
	InputRevision      int64  `json:"input_revision"`
	RunnerAttempt      int64  `json:"runner_attempt"`
}

type DelegatedStopCandidate struct {
	Target           *DelegatedStopTarget `json:"target"`
	FrameID          string               `json:"frame_id"`
	Origin           string               `json:"origin"`
	InSelectedBranch bool                 `json:"in_selected_branch"`
	CanStop          bool                 `json:"can_stop"`
	Status           string               `json:"status"`
	Reason           string               `json:"reason,omitempty"`
}

type DelegatedStopSnapshot struct {
	Schema              string                   `json:"schema"`
	Scope               DelegatedStopScope       `json:"scope"`
	Branch              DelegatedStopBranch      `json:"branch"`
	ParentInputRevision int64                    `json:"parent_input_revision"`
	FenceGeneration     int64                    `json:"fence_generation"`
	CanStopBranch       bool                     `json:"can_stop_branch"`
	Targets             []DelegatedStopCandidate `json:"targets"`
}

type DelegatedStopRequest struct {
	Schema                  string               `json:"schema"`
	RequestID               string               `json:"request_id"`
	Scope                   DelegatedStopScope   `json:"scope"`
	Branch                  DelegatedStopBranch  `json:"branch"`
	ParentInputRevision     int64                `json:"parent_input_revision"`
	ExpectedFenceGeneration int64                `json:"expected_fence_generation"`
	Mode                    string               `json:"mode"`
	Target                  *DelegatedStopTarget `json:"target,omitempty"`
}

type DelegatedStopOutcome struct {
	Target               DelegatedStopTarget `json:"target"`
	Outcome              string              `json:"outcome"`
	ExecutionObservation string              `json:"execution_observation"`
	Diagnostic           string              `json:"diagnostic,omitempty"`
}

// The receipt records selection/cancellation intent, never a second completion
// authority. ExecutionObservation is supplied by the exact live registration.
type DelegatedStopReceipt struct {
	Schema     string                 `json:"schema"`
	Request    DelegatedStopRequest   `json:"request"`
	AcceptedAt time.Time              `json:"accepted_at"`
	State      string                 `json:"state"`
	Replayed   bool                   `json:"replayed"`
	Targets    []DelegatedStopOutcome `json:"targets"`
}

// Admission is internal transient claim authority. ClaimToken never enters a
// frame event, browser response, or recoverable request.
type KernelDelegationAdmission struct {
	ID    string
	Claim transcriptstore.RunnerClaim
}

type delegationAdmissionRecord struct {
	Version                 int                 `json:"version"`
	OwnerID                 string              `json:"owner_id"`
	Scope                   DelegatedStopScope  `json:"scope"`
	Branch                  DelegatedStopBranch `json:"branch"`
	StreamUID               string              `json:"stream_uid"`
	StreamEpoch             int64               `json:"stream_epoch"`
	InputRevision           int64               `json:"input_revision"`
	SourceEventID           int64               `json:"source_event_id"`
	RunnerAttempt           int64               `json:"runner_attempt"`
	ToolUseID               string              `json:"tool_use_id"`
	OuterToolUseID          string              `json:"outer_tool_use_id"`
	RequestSHA256           string              `json:"request_sha256"`
	FenceGeneration         int64               `json:"fence_generation"`
	VisibleFenceGenerations map[string]int64    `json:"visible_fence_generations"`
	ChildIDs                []string            `json:"child_ids,omitempty"`
}

type delegationOriginRecord struct {
	Version       int                 `json:"version"`
	OwnerID       string              `json:"owner_id"`
	Scope         DelegatedStopScope  `json:"scope"`
	AdmissionID   string              `json:"admission_id,omitempty"`
	ToolUseID     string              `json:"tool_use_id"`
	InitialTarget DelegatedStopTarget `json:"initial_target"`
}

type delegationFenceRecord struct {
	Version     int                `json:"version"`
	OwnerID     string             `json:"owner_id"`
	Scope       DelegatedStopScope `json:"scope"`
	StreamUID   string             `json:"stream_uid"`
	StreamEpoch int64              `json:"stream_epoch"`
	BranchID    string             `json:"branch_id"`
	Generation  int64              `json:"generation"`
}
