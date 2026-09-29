package server

import "synon-go/internal/sessionrunner"

// This is a recovery-route signal, not a message filter or task length limit.
// All accepted bytes remain immutable. A few new bytes from an entire truncated
// generation must not continually reset the recovery strategy.
const providerContinuationProgressBytes = sessionrunner.MinRecoverySemanticBytes

func providerContinuationProgressStreak(contracts []sessionRunnerProviderContinuationV1) int {
	streak := 0
	for i := len(contracts) - 1; i >= 0; i-- {
		previous := int64(0)
		if i > 0 {
			previous = contracts[i-1].AcceptedSemanticBytes
		}
		if contracts[i].AcceptedSemanticBytes-previous >= providerContinuationProgressBytes {
			break
		}
		streak++
	}
	return streak
}

func providerContinuationNeedsNextAction(state *sessionRunnerProviderContinuationState) bool {
	return state != nil && state.Contract.SegmentIndex >= 2 && state.ConsecutiveNoProgress >= 2
}
