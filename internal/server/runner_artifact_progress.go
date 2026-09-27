package server

import "strings"

// runnerCorrectionResultMadeMutation distinguishes a successful mutating
// operation from an explicit no-op. Unknown legacy result shapes remain
// progress-capable so this detector cannot reject a real mutation merely
// because an older tool omitted the modern changed/unchanged markers.
func runnerCorrectionResultMadeMutation(result any) bool {
	object := runnerCorrectionResultObject(result)
	if object == nil {
		return true
	}
	if changed, recorded := object["changed"].(bool); recorded && changed {
		return true
	}
	if unchanged, recorded := object["unchanged"].(bool); recorded && !unchanged {
		return true
	}
	artifacts := anySliceValue(object["artifacts"])
	if len(artifacts) > 0 {
		for _, raw := range artifacts {
			artifact := mapValue(raw)
			unchanged, recorded := artifact["unchanged"].(bool)
			if !recorded || !unchanged {
				return true
			}
		}
		return false
	}
	if changed, recorded := object["changed"].(bool); recorded {
		return changed
	}
	if unchanged, recorded := object["unchanged"].(bool); recorded {
		return !unchanged
	}
	return true
}

func runnerArtifactSaveRequiresCorrection(result map[string]any) bool {
	return strings.TrimSpace(stringValue(result["code"])) == "artifact_save_requires_correction" ||
		boolValue(result["completion_pending"], false)
}
