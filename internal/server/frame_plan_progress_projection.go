package server

import "strings"

// compatibilityPlanProgress projects existing execution state; it never decides
// whether a plan can run, pause, complete or pass a scientific check.
func compatibilityPlanProgress(data map[string]any, artifactID, versionID string) map[string]any {
	if artifactID == "" || versionID == "" ||
		strings.TrimSpace(stringValue(data["_plan_artifact_id"])) != artifactID ||
		strings.TrimSpace(stringValue(data["_plan_version_id"])) != versionID {
		return nil
	}
	steps, err := generatedPlanStepIdentities(mapValue(data["_plan_json"]))
	statuses, recorded := data["_step_statuses"].(map[string]any)
	if err != nil || !recorded || statuses == nil {
		return nil
	}
	completed := 0
	seen := make(map[string]bool, len(steps))
	for _, step := range steps {
		if seen[step.ID] {
			return nil
		}
		seen[step.ID] = true
		value, exists := statuses[step.ID]
		if !exists {
			continue
		}
		state, valid := value.(map[string]any)
		if !valid {
			return nil
		}
		if strings.TrimSpace(stringValue(state["status"])) == "completed" {
			completed++
		}
	}
	return map[string]any{"artifact_id": artifactID, "version_id": versionID, "completed": completed, "total": len(steps)}
}
