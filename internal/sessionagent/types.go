// Package sessionagent defines the private, durable Agent binding contract used
// by the existing workspace input and Transcript claim owners. It is not a
// second scheduler or a provider runtime.
package sessionagent

import (
	"errors"
	"time"
)

const (
	Schema            = "x-science.session-specialist.v1"
	CommandSchema     = "x-science.session-specialist-command.v1"
	MutationSchema    = "x-science.session-specialist-mutation.v1"
	SnapshotSchema    = "x-science.session-agent-snapshot.v1"
	HeadEventType     = "session_specialist_binding"
	ReceiptEventType  = "session_specialist_command"
	SnapshotEventType = "session_agent_snapshot"
	InputEventType    = "session_agent_input_binding"
	ClaimEventType    = "session_agent_claim_binding"
	AppliedEventType  = "session_agent_application"
)

var (
	ErrInvalid           = errors.New("SESSION_SPECIALIST_INVALID")
	ErrUnavailable       = errors.New("SESSION_SPECIALIST_UNAVAILABLE")
	ErrTargetUnavailable = errors.New("SESSION_SPECIALIST_TARGET_UNAVAILABLE")
	ErrScopeStale        = errors.New("SESSION_SPECIALIST_SCOPE_STALE")
	ErrRevisionConflict  = errors.New("SESSION_SPECIALIST_REVISION_CONFLICT")
	ErrCommandConflict   = errors.New("SESSION_SPECIALIST_COMMAND_CONFLICT")
	ErrPending           = errors.New("SESSION_SPECIALIST_PENDING")
)

// A selection changes this conversation frame, never all delegated children of
// its root. Root identity is an additional lifetime/ownership fence.
type Scope struct {
	FrameID            string `json:"frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	RootFrameID        string `json:"root_frame_id"`
	RootIncarnationID  string `json:"root_incarnation_id"`
}

type Command struct {
	Schema           string `json:"schema"`
	CommandID        string `json:"command_id"`
	Scope            Scope  `json:"scope"`
	ExpectedRevision int64  `json:"expected_revision"`
	Branch           Branch `json:"branch"`
	DesiredAgent     string `json:"desired_agent"`
	UseDefault       bool   `json:"use_default,omitempty"`
}

type Branch struct {
	ID         string `json:"id"`
	Generation int64  `json:"generation"`
}

type Selection struct {
	AgentName     string `json:"agent_name"`
	SelectionName string `json:"selection_name"`
	AssistantID   string `json:"assistant_id"`
	DisplayName   string `json:"display_name"`
	IsDefault     bool   `json:"is_default"`
	SnapshotID    string `json:"snapshot_id"`
	Revision      int64  `json:"revision"`
}

// InputReference is an intent reference, never a caller-supplied definition.
// Admission resolves it against the private prepared record in its own write
// transaction, after accepted-input replay has already been checked.
type InputReference struct {
	Scope      Scope  `json:"scope"`
	Branch     Branch `json:"branch"`
	Revision   int64  `json:"revision"`
	SnapshotID string `json:"snapshot_id"`
}

type Readback struct {
	Schema             string    `json:"schema"`
	Scope              Scope     `json:"scope"`
	Revision           int64     `json:"revision"`
	Branch             Branch    `json:"branch"`
	PreparedBranch     Branch    `json:"prepared_branch"`
	Desired            Selection `json:"desired"`
	Applied            Selection `json:"applied"`
	Phase              string    `json:"phase"`
	HasLiveAttempt     bool      `json:"has_live_attempt"`
	PreparationError   string    `json:"preparation_error,omitempty"`
	AppliedUnavailable bool      `json:"applied_unavailable"`
}

type Preparation struct {
	Desired             Snapshot
	Fallback            *Snapshot
	FallbackUnavailable bool
}

// An accepted receipt is immutable; actual runtime application is reported by
// the separate current readback, not by overwriting this receipt on a retry.
type Receipt struct {
	Command          Command   `json:"command"`
	AcceptedRevision int64     `json:"accepted_revision"`
	SnapshotID       string    `json:"snapshot_id"`
	AcceptedAt       time.Time `json:"accepted_at"`
}

type Mutation struct {
	Schema   string   `json:"schema"`
	Receipt  Receipt  `json:"receipt"`
	Snapshot Readback `json:"snapshot"`
	Replayed bool     `json:"replayed"`
}

// Snapshot contains only named, non-secret Agent configuration. No provider,
// connector, environment, arbitrary defaults map or credential value is copied.
// Models/connectors below are catalog IDs/references; their existing runtime
// authority and approval owners still resolve and constrain their use.
type Snapshot struct {
	Schema             string              `json:"schema"`
	ID                 string              `json:"id"`
	AgentName          string              `json:"agent_name"`
	SelectionName      string              `json:"selection_name"`
	IsDefault          bool                `json:"is_default"`
	SelectionProfileID string              `json:"selection_profile_id,omitempty"`
	DefinitionID       string              `json:"definition_id"`
	AssistantID        string              `json:"assistant_id"`
	DisplayName        string              `json:"display_name"`
	Bundled            *BundledDefinition  `json:"bundled,omitempty"`
	Profile            *PersonalDefinition `json:"profile,omitempty"`
	Settings           Settings            `json:"settings"`
}

type BundledDefinition struct {
	SystemPrompt       string   `json:"system_prompt"`
	SkillNames         []string `json:"skill_names"`
	ConnectorIDs       []string `json:"connector_ids"`
	ExcludedTools      []string `json:"excluded_tools"`
	SkillsLocked       bool     `json:"skills_locked"`
	SupportsPlanMode   bool     `json:"supports_plan_mode"`
	Unrestricted       bool     `json:"unrestricted"`
	EnableThinking     bool     `json:"enable_thinking"`
	EnableWebSearch    bool     `json:"enable_web_search"`
	EnableWebFetch     bool     `json:"enable_web_fetch"`
	MaxToolResultChars int      `json:"max_tool_result_chars"`
}

type PersonalDefinition struct {
	SystemPrompt        string   `json:"system_prompt"`
	SkillNames          []string `json:"skill_names"`
	SkillTombstones     []string `json:"skill_tombstones"`
	ConnectorTombstones []string `json:"connector_tombstones"`
	Unrestricted        bool     `json:"unrestricted"`
}

type Settings struct {
	Model                 string   `json:"model"`
	ThoughtLevel          string   `json:"thought_level"`
	CustomSkillNames      []string `json:"custom_skill_names"`
	DisabledBuiltinSkills []string `json:"disabled_builtin_skills"`
	DefaultSkillNames     []string `json:"default_skill_names"`
	DefaultSkillsFixed    bool     `json:"default_skills_fixed"`
	AllowedSkillNames     []string `json:"allowed_skill_names"`
	AllowedMCPIDs         []string `json:"allowed_mcp_ids"`
	AllowedModels         []string `json:"allowed_models"`
	CustomPrompt          string   `json:"custom_prompt"`
}
