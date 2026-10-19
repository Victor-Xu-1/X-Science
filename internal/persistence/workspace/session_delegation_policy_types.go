package workspace

import (
	"errors"
	"time"
)

const (
	SessionDelegationPolicySchema   = "x-science.session-delegation-policy.v1"
	SessionDelegationCommandSchema  = "x-science.session-delegation-command.v1"
	SessionDelegationReceiptSchema  = "x-science.session-delegation-receipt.v1"
	SessionDelegationMutationSchema = "x-science.session-delegation-mutation.v1"

	sessionDelegationPolicyEventType  = "session_delegation_policy"
	sessionDelegationCommandEventType = "session_delegation_command"
)

var (
	ErrSessionDelegationInvalid     = errors.New("SESSION_DELEGATION_INVALID")
	ErrSessionDelegationTarget      = errors.New("SESSION_DELEGATION_TARGET_UNAVAILABLE")
	ErrSessionDelegationUnavailable = errors.New("SESSION_DELEGATION_UNAVAILABLE")
	ErrSessionDelegationScopeStale  = errors.New("SESSION_DELEGATION_SCOPE_STALE")
	ErrSessionDelegationConflict    = errors.New("SESSION_DELEGATION_COMMAND_CONFLICT")
	ErrSessionDelegationRevision    = errors.New("SESSION_DELEGATION_REVISION_CONFLICT")
	ErrSessionDelegationDisabled    = errors.New("SESSION_DELEGATION_DISABLED")
)

// Policy authority belongs to one durable root lifetime, not an input snapshot,
// a model instruction, a selected Message Branch, or an in-memory child runner.
type SessionDelegationScope struct {
	RootFrameID       string `json:"root_frame_id"`
	RootIncarnationID string `json:"root_incarnation_id"`
}

type SessionDelegationSnapshot struct {
	Schema                   string                 `json:"schema"`
	Scope                    SessionDelegationScope `json:"scope"`
	Policy                   string                 `json:"policy"`
	Revision                 int64                  `json:"revision"`
	HasLiveDelegatedAttempts bool                   `json:"has_live_delegated_attempts"`
}

type SessionDelegationCommand struct {
	Schema           string                 `json:"schema"`
	CommandID        string                 `json:"command_id"`
	Scope            SessionDelegationScope `json:"scope"`
	ExpectedRevision int64                  `json:"expected_revision"`
	DesiredPolicy    string                 `json:"desired_policy"`
}

// The receipt is immutable. A retry returns it without changing policy again;
// Snapshot is a separate current readback and may be newer than this receipt.
type SessionDelegationReceipt struct {
	Schema          string                   `json:"schema"`
	Command         SessionDelegationCommand `json:"command"`
	AppliedRevision int64                    `json:"applied_revision"`
	AppliedPolicy   string                   `json:"applied_policy"`
	AppliedAt       time.Time                `json:"applied_at"`
}

type SessionDelegationMutation struct {
	Schema   string                    `json:"schema"`
	Receipt  SessionDelegationReceipt  `json:"receipt"`
	Snapshot SessionDelegationSnapshot `json:"snapshot"`
	Replayed bool                      `json:"replayed"`
}

type SessionDelegationRevisionError struct {
	Snapshot SessionDelegationSnapshot
}

func (e *SessionDelegationRevisionError) Error() string { return ErrSessionDelegationRevision.Error() }
func (e *SessionDelegationRevisionError) Unwrap() error { return ErrSessionDelegationRevision }
