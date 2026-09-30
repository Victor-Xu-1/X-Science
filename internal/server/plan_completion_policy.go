package server

import "log"

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
