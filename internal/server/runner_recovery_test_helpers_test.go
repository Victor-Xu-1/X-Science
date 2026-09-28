package server

import (
	eventjournal "synon-go/internal/persistence/journal"
	transcriptstore "synon-go/internal/persistence/transcript"
)

// Count only the current unchanged obligation. A new typed obligation or user
// task opens a new scope; unrelated tool successes and clarifications do not.
func runnerRepeatedCorrectionInterruptionCount(entries []eventjournal.Entry, correction transcriptstore.RunnerInterruptionCause) int {
	var state runnerCorrectionRepetition
	for _, entry := range entries {
		state.observe(entry)
	}
	if state.Fingerprint != runnerCorrectionFingerprint(correction) {
		return 0
	}
	return state.Count
}
func runnerNoProgressRecoveryFromEntries(
	entries []eventjournal.Entry,
	authority *transcriptRunnerAuthority,
) sessionRunnerNoProgressRecovery {
	state := newSessionRunnerNoProgressRecovery(runnerRecoveryObligationFingerprint(authority, transcriptstore.RunnerInterruptionCause{}))
	for _, entry := range entries {
		state.observeEntry(entry, authority)
	}
	return state
}
