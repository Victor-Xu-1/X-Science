package server

import "x-science/internal/agentruntime"

func sourceObservationIsUnchanged(observation *agentruntime.SourceObservation, sourceCheckpoint bool) bool {
	return sourceCheckpoint && observation == nil || observation != nil && observation.HasSources && observation.State == agentruntime.SourceObservationRepeated
}

// Receipt integrity proves origin/content, not a successful source outcome.
// Use the existing closed envelope contract on restored original content.
func runnerSourceReceiptSucceeded(value any) bool {
	return !agentruntime.ToolResultDidNotExecute(value) && agentruntime.ClassifyToolResult(value) == agentruntime.ToolResultSucceeded
}
