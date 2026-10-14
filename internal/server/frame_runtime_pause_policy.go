package server

import transcriptstore "x-science/internal/persistence/transcript"

// runnerInterruptionProjectsPaused consumes durable resume authority, not clock
// availability. Model selection retains its separate fresh-input recovery path.
func runnerInterruptionProjectsPaused(interruption transcriptstore.RunnerInterruption, checkpoint transcriptstore.RunnerCheckpoint, resumable, modelSelectionWait bool) bool {
	if modelSelectionWait {
		return true
	}
	if !interruption.AutoResume {
		return resumable && checkpoint.Sequence == interruption.CheckpointSequence
	}
	return !resumable || checkpoint.Sequence != interruption.CheckpointSequence
}
