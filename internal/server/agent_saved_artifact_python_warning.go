package server

import workspace "x-science/internal/persistence/workspace"

// Attach only after the canonical write or unchanged association has succeeded.
// No symbol, source, interpreter path or diagnostic text crosses this boundary.
func agentSavedPythonAnalysisWarning(path string, version workspace.ArtifactVersion) map[string]any {
	return map[string]any{
		"code": "python_static_analysis_indeterminate", "severity": "warning",
		"validation_stage": "python_validation", "validation_code": "analysis_indeterminate",
		"path": path, "version_id": version.ID, "sha256": version.ContentSHA256,
		"message": "The Python source was saved. Static analysis could not prove all global names are defined because the source uses a dynamic module namespace. This is not execution certification.",
	}
}
