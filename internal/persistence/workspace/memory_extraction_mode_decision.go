package workspace

import (
	"errors"
	"x-science/internal/memoryconfig"
)

const (
	MemoryModeConfigured     = "configured"
	MemoryModeForkedOversize = "forked-view-oversize"
)

// Optional on historical records. Absence means unrecorded, never inferred auto.
type MemoryExtractionModeDecision struct {
	RequestedMode      string                          `json:"requested_mode"`
	EffectiveMode      string                          `json:"effective_mode"`
	Reason             string                          `json:"reason"`
	RecoveryPolicy     memoryconfig.ForkedViewRecovery `json:"recovery_policy"`
	RequestAttempt     int64                           `json:"request_attempt"`
	ModelProfileSHA256 string                          `json:"model_profile_sha256,omitempty"`
}

func (decision *MemoryExtractionModeDecision) Clone() *MemoryExtractionModeDecision {
	if decision == nil {
		return nil
	}
	copy := *decision
	return &copy
}

func (decision *MemoryExtractionModeDecision) Recovered() bool {
	return decision != nil && decision.Reason == MemoryModeForkedOversize
}

func (decision *MemoryExtractionModeDecision) Validate(source CompletedExtractionSource) error {
	if decision == nil {
		return nil
	}
	invalid := errors.New("invalid Memory extraction mode decision")
	if !decision.RecoveryPolicy.Valid() || source.Transcript == nil || decision.RequestAttempt <= 0 ||
		decision.RequestAttempt != source.Transcript.Attempt {
		return invalid
	}
	for _, mode := range []string{decision.RequestedMode, decision.EffectiveMode} {
		if mode != "forked" && mode != "compact" {
			return invalid
		}
	}
	if decision.ModelProfileSHA256 != "" && normalizedExtractionDigest(decision.ModelProfileSHA256) != decision.ModelProfileSHA256 {
		return invalid
	}
	switch decision.Reason {
	case MemoryModeConfigured:
		if decision.RequestedMode != decision.EffectiveMode {
			return invalid
		}
	case MemoryModeForkedOversize:
		if decision.RequestedMode != "forked" || decision.EffectiveMode != "compact" ||
			decision.RecoveryPolicy != memoryconfig.ForkedViewRecoveryCompact || decision.ModelProfileSHA256 == "" {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}
