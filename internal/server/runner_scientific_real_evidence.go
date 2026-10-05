package server

import (
	"strings"

	eventjournal "synon-go/internal/persistence/journal"
	transcriptstore "synon-go/internal/persistence/transcript"
)

// Retained only so an older durable correction can be decoded and resumed.
// New completion decisions are derived from typed Tool, Skill, plan, artifact,
// and operation state and never emit this task-text-classifier reason.
const sessionRunnerRealScientificEvidenceRequiredReasonCode = "real_scientific_evidence_required"

func artifactReferencesFromRunnerEntries(entries []eventjournal.Entry) []transcriptstore.ArtifactReferenceInput {
	result := make([]transcriptstore.ArtifactReferenceInput, 0)
	seen := make(map[string]struct{})
	for _, entry := range entries {
		toolResult, _ := entry.Message["toolResult"].(map[string]any)
		if toolResult == nil {
			toolResult, _ = entry.Message["tool_result"].(map[string]any)
		}
		artifacts, _ := toolResult["artifacts"].([]any)
		for _, value := range artifacts {
			artifact, _ := value.(map[string]any)
			artifactID := strings.TrimSpace(firstNonEmpty(
				stringValue(artifact["artifact_id"]), stringValue(artifact["artifactId"]),
			))
			versionID := strings.TrimSpace(firstNonEmpty(
				stringValue(artifact["version_id"]), stringValue(artifact["versionId"]),
			))
			if artifactID == "" || versionID == "" {
				continue
			}
			key := artifactID + "\x00" + versionID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, transcriptstore.ArtifactReferenceInput{
				ArtifactID: artifactID,
				VersionID:  versionID,
				Relation:   transcriptstore.ArtifactRelationProduced,
			})
		}
	}
	return result
}

func sessionRunnerHasAuthoritativeSourceEvidence(signals []string) bool {
	for _, signal := range normalizeTrustedScientificReviewSignals(signals) {
		switch {
		case strings.HasPrefix(signal, "source-connector:"),
			strings.HasPrefix(signal, "source-authority:"),
			strings.HasPrefix(signal, trustedScientificEvidenceRecordSignalPrefix),
			signal == "scientific-tool:binding_mode_analysis":
			return true
		}
	}
	return false
}
