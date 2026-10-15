package server

import (
	"errors"
	"fmt"

	"x-science/internal/persistence/workspace"
)

// The broad historical unavailable sentinel also includes protocol and input
// failures. Only this closed subtype authorizes an optional-capability notice.
type scientificArtifactCapabilityUnavailable struct{ reason string }

func (err *scientificArtifactCapabilityUnavailable) Error() string {
	return fmt.Sprintf("%s: %s", errScientificArtifactValidationUnavailable, err.reason)
}

func (err *scientificArtifactCapabilityUnavailable) Unwrap() error {
	return errScientificArtifactValidationUnavailable
}

func optionalScientificValidationReason(err error) (string, bool) {
	var unavailable *scientificArtifactCapabilityUnavailable
	if !errors.As(err, &unavailable) || unavailable == nil {
		return "", false
	}
	switch unavailable.reason {
	case "not_configured", "unsupported_platform", "runtime_not_installed", "validator_not_installed", "parser_unavailable":
		return unavailable.reason, true
	default:
		return "", false
	}
}

type scientificArtifactUnavailableObservation struct {
	VersionID string
	SHA256    string
	Reason    string
}

type scientificArtifactCompletionValidation struct {
	Failures               []string
	AggregateCountFindings []string
	Unavailable            []scientificArtifactUnavailableObservation
	Selection              []scientificArtifactIdentity
}

type scientificArtifactIdentity struct {
	ArtifactID string
	VersionID  string
	SHA256     string
	Format     string
}

func (validation scientificArtifactCompletionValidation) advisory() *sessionRunnerCompletionAdvisory {
	if len(validation.Unavailable) == 0 {
		return nil
	}
	findings := make([]string, 0, len(validation.Unavailable))
	for _, observation := range validation.Unavailable {
		findings = append(findings, observation.VersionID+":"+observation.SHA256+":"+observation.Reason)
	}
	return &sessionRunnerCompletionAdvisory{Code: completionAdvisoryChemicalValidation, Findings: findings}
}

func (validation scientificArtifactCompletionValidation) aggregateCountAdvisory() *sessionRunnerCompletionAdvisory {
	if len(validation.AggregateCountFindings) == 0 {
		return nil
	}
	return &sessionRunnerCompletionAdvisory{Code: completionAdvisoryResultQuality,
		Findings: append([]string(nil), validation.AggregateCountFindings...)}
}

func scientificArtifactUnavailableWarning(path, reason string, version workspace.ArtifactVersion) map[string]any {
	return map[string]any{
		"code": "scientific_validation_unavailable", "severity": "warning", "validation_status": "not_performed",
		"path": path, "version_id": version.ID, "sha256": version.ContentSHA256, "reason": reason,
		"message": "The existing file was saved unchanged. Optional chemical validation was not performed; molecular validity has not been certified.",
	}
}
