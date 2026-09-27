package server

// A tool's durable origin owns its public identity, while the latest observing
// runner attempt owns its current execution. They diverge after an explicit
// resume and must not be conflated when that later attempt ends.
func transcriptToolExecutionAttempt(fact transcriptToolHistoryFact) int64 {
	if fact.observedAttempt > 0 {
		return fact.observedAttempt
	}
	return fact.attempt
}

func transcriptToolResumesSyntheticSettlement(current, update transcriptToolHistoryFact) bool {
	if current.resultSet || !update.explicitOriginAttempt || current.attempt != update.attempt ||
		transcriptToolExecutionAttempt(update) <= transcriptToolExecutionAttempt(current) {
		return false
	}
	// Only an inferred end-of-attempt UI settlement is replaceable. A real tool
	// terminal receipt never acquires these markers and remains irreversible.
	// The explicit durable origin and strictly newer execution are the proof of
	// continuation; unchanged identity, input and parent are checked by the merge.
	switch current.settlement {
	case "task_cancelled":
		return current.status == "canceled"
	case "task_completed_without_tool_receipt", "task_failed_without_tool_receipt", "execution_interrupted":
		return current.status == "interrupted"
	default:
		return false
	}
}
