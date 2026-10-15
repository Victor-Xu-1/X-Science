package server

import (
	"log"

	"x-science/internal/agentruntime"
)

// Text observations cannot establish execution intent or certify fulfilment.
// Keep recorded outcomes intact and disclose the limitation in the existing
// final candidate before its review/publication identity is bound.
func sessionRunnerTaskTextAdvisory(run *sessionRunnerChatRun, request, fresh, all []agentruntime.Message, final string) *sessionRunnerCompletionAdvisory {
	observations := buildSessionRunnerExplicitToolContract(sessionRunnerTaskIntent(run))
	findings := observations.gaps(all)
	findings = append(findings, observations.finalGaps(all, final)...)
	if finding := sessionRunnerFreshExecutionFinding(request, fresh); finding != "" {
		findings = append(findings, finding)
	}
	if run != nil && run.SemanticReviewObserved {
		findings = append(findings, "a prior semantic review was preserved without certifying its claims")
	}
	if len(findings) == 0 {
		return nil
	}
	log.Printf("runner_completion_advisory_task_expectations count=%d", len(findings))
	if len(findings) > 32 {
		findings = findings[:32]
	}
	return &sessionRunnerCompletionAdvisory{Code: completionAdvisoryTaskExpectations, Findings: findings}
}

// A Review body records semantic claims, not a typed operation admission.
// Retiring its force-to-call projection only enters ordinary continuation;
// current operation/artifact/persistence checks still own completion. Legacy
// text is likewise retained as history, never promoted to a recovered answer.
func sessionRunnerSemanticReviewIsAdvisory(correction recoveredRunnerCorrection) bool {
	if correction.ReasonCode != "completion_review_correction_required" {
		return false
	}
	condition := correction.Condition
	return condition == nil || condition.ReasonCode == correction.ReasonCode &&
		condition.Validate() == nil && condition.Review != nil
}
