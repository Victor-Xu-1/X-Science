package server

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/providers"
	"x-science/internal/sciencecapability"
	"x-science/internal/sessionagent"
	runnermachine "x-science/internal/sessionrunner"
)

type SessionRunnerChatOptions struct {
	agentSnapshot                     *sessionagent.Snapshot
	expectedInputClaim                *runnerInputClaimTarget
	SessionID                         string
	RunnerID                          string
	Endpoint                          string
	APIKey                            string
	Model                             string
	SystemPrompt                      string
	AllowedTools                      []string
	SelectedSkillNames                []string
	ExcludedSkillNames                []string
	AllowedSkillNames                 []string
	RestrictSkillDiscovery            bool
	RequestTimeout                    time.Duration
	PreparationTimeout                time.Duration
	CompactSummaryTimeout             time.Duration
	MaxToolRounds                     int
	MaxToolCallsPerRound              int
	MaxConsecutiveIdenticalToolRounds int
	MaxAttempts                       int
	LeaseTTL                          time.Duration
	PollInterval                      time.Duration
	ReplayLimit                       int64
	OutputLimitBytes                  int64
	ModelResponseLimitBytes           int64
	TranscriptResumeSource            transcriptstore.ResumeSource
	TranscriptCheckpoint              int64
	DisableSkillDiscovery             bool
	DisableMCPDiscovery               bool
	DisableThinking                   bool
	ModelProfile                      *providers.ModelProfile
	ModelAudit                        func(providers.AuditRecord)
	RequireSavedModel                 bool
	// modelSelection and modelSelectionRevision bind the profile resolved by
	// the runner admission step to the first provider call. The dynamic model
	// client still re-resolves after a failed call or a live model switch, but
	// the first call must not repeat the same provider/profile lookup.
	modelSelection         string
	modelSelectionRevision int64
	modelOwnerUserID       string
	sourceToolActivity     *sessionRunnerSourceToolActivity
	RuntimeSessionConfig   map[string]any
}

type sessionRunnerChatRun struct {
	SessionID          string
	Attempt            int
	ClaimToken         string
	AfterEventID       int64
	Transcript         *transcriptRunnerAuthority
	TaskIntent         string
	TaskIntentID       string
	TaskIntentRevision int64
	currentControl     *sessionRunnerCurrentControl
	// frameOwnedJob is set only by validated internal-job admission. Its
	// dedicated frame, not a user-message task intent, bounds durable receipts.
	frameOwnedJob                      bool
	ResponseLanguage                   string
	ToolSourceEventIDs                 map[string]int64
	KernelOperationIDs                 map[string]string
	ToolBatchIDs                       map[string]string
	ToolBatchOrdinals                  map[string]int64
	AssistantSegmentOrdinal            int64
	AssistantSegmentHasContent         bool
	AssistantSegmentContent            strings.Builder
	PlanModeDenials                    int
	AutonomousPlanning                 bool
	planProgressAvailable              atomic.Bool
	ProviderContinuation               *sessionRunnerProviderContinuationState
	ProviderAttemptSemanticBytes       int64
	generationRecoveryMu               sync.Mutex
	generationRecovery                 providerGenerationRecovery
	NoProgressRecovery                 *sessionRunnerNoProgressRecovery
	sourceObservationMu                sync.Mutex
	sourceObservations                 *runnerSourceObservationProjection
	sourceObservationResultCallID      string
	sourceObservationResult            *agentruntime.SourceObservation
	sourceProgressReplay               runnerSourceObservationReducer
	managedEnvironmentMu               sync.Mutex
	managedEnvironmentBindings         map[string]string
	managedEnvironmentImplementations  map[string][]string
	managedEnvironmentInvalidations    map[string]managedEnvironmentInvalidation
	managedEnvironmentBindingsHydrated bool
	managedEnvironmentHydration        *managedEnvironmentHydration
	readReuse                          *sessionRunnerReadReuseCache
	scientificCapabilitiesMu           sync.Mutex
	toolCapabilityCatalog              map[string][]string
	activatedToolNames                 []string
	activatedToolCapabilities          []string
	InputAttachmentReaders             []sessionRunnerInputAttachmentReader
	executedSkillInvocationKeys        map[string]struct{}
	ReviewPolicy                       *sessionRunnerResolvedReviewPolicy
	// Per-run presentation observation only; no execution or resume authority.
	SemanticReviewObserved bool
	// VerificationExplicitlyDisabled records the resolved user-owned
	// verifier mode. It keeps ordinary completion and artifact saving from
	// falling back to legacy strict-review behavior when review is off.
	VerificationExplicitlyDisabled       bool
	RequiredScientificCapabilities       []string
	ExecutedSkillNames                   []string
	PendingRequiredSkillNames            []string
	SelectedImplementations              []string
	SelectedEvidenceResolvers            []sciencecapability.ExecutionEvidenceResolver
	RegistrySelectedImplementations      []string
	ResolvedUserEvidence                 map[string]string
	ImplementationSelectionRequired      bool
	TrustedScientificReviewSignals       []string
	TrustedScientificCapabilityWitnesses []sciencecapability.Witness
	ContinuationArtifactReferences       []transcriptstore.ArtifactReferenceInput
	ScientificEvidenceVerified           bool
	CorrectionReason                     string
	CorrectionDetail                     string
	CorrectionCondition                  *transcriptstore.RunnerCorrectionCondition
	requiredMCPSourceClass               string
	phaseMachine                         *runnermachine.Machine
	// SuppressReviewCheckpoints keeps a detached, user-triggered review from
	// appending checkpoints to an already-terminal main runner. The reviewer
	// still owns its hidden child-frame transcript and durable verification
	// records; the completed task transcript remains immutable.
	SuppressReviewCheckpoints bool
}
