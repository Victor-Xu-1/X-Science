package server

import (
	"log"
	"strings"
)

// Autonomous plan statuses are navigation, not a second completion oracle.
// The same final candidate has already passed task/receipt/artifact checks.
// Retain unfinished navigation without forcing another bookkeeping/model round;
// explicitly reviewed plans keep their existing execution obligations.
func (s *Server) validateGeneratedPlanCompletion(frameID string, run *sessionRunnerChatRun) error {
	remaining, err := s.incompleteGeneratedPlanCondition(frameID, run)
	if err != nil || remaining == nil {
		return err
	}
	if !remaining.advisory {
		return *remaining
	}
	log.Printf("runner_completion_advisory_plan_steps count=%d", len(remaining.condition.Steps))
	return nil
}

func (s *Server) recoveredCompletionCorrectionIsAdvisory(
	frameID string, run *sessionRunnerChatRun, correction recoveredRunnerCorrection,
) (bool, error) {
	if correction.ReasonCode != sessionRunnerPlanStepsIncompleteReasonCode {
		return sessionRunnerRecoveredCorrectionIsAdvisory(correction.ReasonCode, correction.repairDetail()), nil
	}
	// Only the typed host condition can identify an old plan obligation. Do
	// not infer plan identity or authorization from its human-readable detail.
	if correction.Condition == nil || correction.Condition.ReasonCode != correction.ReasonCode ||
		correction.Condition.Validate() != nil || correction.Condition.Plan == nil {
		return false, nil
	}
	contextData, err := s.generatedPlanCompletionContext(frameID, run)
	if err != nil || contextData == nil || !autonomousGeneratedPlan(contextData) {
		return false, err
	}
	plan := correction.Condition.Plan
	return strings.TrimSpace(plan.ArtifactID) != "" && strings.TrimSpace(plan.VersionID) != "" &&
		plan.ArtifactID == stringValue(contextData["_plan_artifact_id"]) &&
		plan.VersionID == stringValue(contextData["_plan_version_id"]), nil
}

// Completion and recovery share this scoped metadata authority. In particular,
// completed navigation must not revive an older administrative interruption.
func (s *Server) generatedPlanCompletionContext(frameID string, run *sessionRunnerChatRun) (map[string]any, error) {
	if s == nil || s.workspaceStore == nil || strings.TrimSpace(frameID) == "" {
		return nil, nil
	}
	metadata, found, err := s.workspaceStore.GetFrameRuntimeMetadata(strings.TrimSpace(frameID))
	if err != nil || !found {
		return nil, err
	}
	contextData := mapValue(metadata.ContextData)
	if !generatedPlanExecutionAuthorized(contextData) || (run != nil && !sessionRunnerPlanMatchesTask(contextData, run)) {
		return nil, nil
	}
	return contextData, nil
}
