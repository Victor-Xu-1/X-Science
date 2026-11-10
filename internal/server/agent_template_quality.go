package server

import (
	"fmt"
	"strings"

	workspace "x-science/internal/persistence/workspace"
)

// The scanner detects presentation forms, not executable syntax or successful
// rendering. Malformed artifact placeholders have a separate integrity code.
// Save, fresh completion and historical typed recovery use this one policy.
func sessionRunnerTemplateQualityFinding(finding string) bool {
	const prefix = "unresolved_template_marker:"
	finding = strings.ToLower(strings.TrimSpace(finding))
	return strings.HasPrefix(finding, prefix) && strings.TrimSpace(strings.TrimPrefix(finding, prefix)) != ""
}

type agentSavedArtifactTemplateFindings struct {
	failures []string
}

func (finding *agentSavedArtifactTemplateFindings) Error() string {
	return fmt.Sprintf("%s: %s", errAgentSavedArtifactTemplateUnresolved, strings.Join(finding.failures, ", "))
}

func (finding *agentSavedArtifactTemplateFindings) Unwrap() error {
	return errAgentSavedArtifactTemplateUnresolved
}

func agentSavedArtifactTemplateQualityFindings(err error) []string {
	finding, checkerFinding := err.(*agentSavedArtifactTemplateFindings)
	if !checkerFinding || finding == nil || len(finding.failures) == 0 {
		return nil
	}
	for _, failure := range finding.failures {
		if !sessionRunnerTemplateQualityFinding(failure) {
			return nil
		}
	}
	return append([]string(nil), finding.failures...)
}

// Called only after the original save/association transaction succeeds. The
// warning describes that committed immutable version, never a promised save.
func agentSavedArtifactTemplateWarning(path, artifactID string, version workspace.ArtifactVersion, findings []string) map[string]any {
	if len(findings) == 0 {
		return nil
	}
	return map[string]any{
		"code": "artifact_consistency_advisory", "severity": "warning",
		"blocking": false, "quality_advisory": true, "completion_blocking": false,
		"path": path, "artifact_id": artifactID, "version_id": version.ID, "sha256": version.ContentSHA256,
		"findings": append([]string(nil), findings...), "finding_count": len(findings),
		"message": "Template markers are retained in the saved version; rendered content quality remains unverified.",
	}
}
