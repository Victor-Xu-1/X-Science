package server

import "strings"

func sessionRunnerQualityFindingIsAdvisory(finding string) bool {
	return sessionRunnerCompletionAdvisoryCrossArtifactFailure(finding) ||
		sessionRunnerEvidenceGapCrossArtifactFailure(finding) ||
		scientificArtifactAggregateCountFindingIsAdvisory(finding)
}

// The result retains its observations, including failed checks. This bounded
// notice reports their limitation without exposing raw diagnostics as prompt
// instructions or creating another repair obligation.
func sessionRunnerCompletionQualityAdvisory(run *sessionRunnerChatRun, failures, unsupportedCitations []string) *sessionRunnerCompletionAdvisory {
	if run == nil {
		return nil
	}
	const maxNoticeFindings = 32
	var findings []string
	for _, failure := range failures {
		if sessionRunnerQualityFindingIsAdvisory(failure) {
			findings = append(findings, failure)
			if len(findings) == maxNoticeFindings {
				break
			}
		}
	}
	for _, reference := range unsupportedCitations {
		if len(findings) == maxNoticeFindings {
			break
		}
		if strings.TrimSpace(reference) != "" {
			findings = append(findings, "unsupported_citation:"+reference)
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return &sessionRunnerCompletionAdvisory{Code: completionAdvisoryResultQuality, Findings: findings}
}

// Old typed obligations use the same policy as fresh completion, never the
// truncated presentation detail. A mixed integrity/quality condition remains
// hard; removing its display text must not remove an actual missing output.
func sessionRunnerReferenceCorrectionIsAdvisory(run *sessionRunnerChatRun, correction recoveredRunnerCorrection) bool {
	condition := correction.Condition
	if run == nil || correction.ReasonCode != "artifact_reference_correction_required" || condition == nil ||
		condition.ReasonCode != correction.ReasonCode || condition.Validate() != nil || condition.Reference == nil {
		return false
	}
	reference := condition.Reference
	if reference.UnresolvedArtifacts != 0 || len(reference.UnresolvedArtifactReferences) != 0 ||
		len(reference.MalformedArtifactReferences) != 0 || len(reference.InvalidReferenceArtifacts) != 0 ||
		len(reference.InvalidResearchArtifacts) != 0 ||
		len(reference.MissingLocalArtifacts) != 0 || len(reference.MissingRequiredDeliverables) != 0 {
		return false
	}
	findings := append([]string(nil), reference.CrossArtifactFailures...)
	for _, failure := range reference.InvalidScientificArtifacts {
		if !scientificArtifactAggregateCountFindingIsAdvisory(failure) {
			return false
		}
		findings = append(findings, failure)
	}
	for _, failure := range reference.CrossArtifactFailures {
		if !sessionRunnerQualityFindingIsAdvisory(failure) {
			return false
		}
	}
	return sessionRunnerCompletionQualityAdvisory(run, findings, reference.UnsupportedCitations) != nil
}
