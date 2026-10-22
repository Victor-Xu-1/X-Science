package server

import (
	"fmt"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

type contextInspectionOutcome struct {
	ID              string              `json:"id"`
	Status          string              `json:"status"`
	TerminalEventID int64               `json:"terminalEventId"`
	CompletedAt     time.Time           `json:"completedAt"`
	RunnerAttempt   int64               `json:"runnerAttempt"`
	CallIDs         []string            `json:"callIds"`
	Sample          *runnerContextUsage `json:"sample,omitempty"`
}

func populateContextInspectionOutcomes(result *contextInspection, authority transcriptstore.ContextInspectionSnapshot, turns map[int64]int, samples []runnerContextUsage) {
	byID := map[string]runnerContextUsage{}
	for _, sample := range samples {
		byID[sample.RequestID] = sample
	}
	for _, fact := range authority.Outcomes {
		index, found := turns[fact.InputEventID]
		if !found {
			continue
		}
		outcome := contextInspectionOutcome{ID: fmt.Sprintf("%s:terminal:%d", authority.Scope.StreamUID, fact.TerminalEventID),
			Status: fact.Status, TerminalEventID: fact.TerminalEventID, CompletedAt: fact.CompletedAt, RunnerAttempt: fact.Attempt, CallIDs: []string{}}
		if !fact.Retired {
			for _, call := range result.Calls {
				if call.InputEventID != fact.InputEventID || int64(call.RunnerAttempt) != fact.Attempt {
					continue
				}
				outcome.CallIDs = append(outcome.CallIDs, call.ID)
				if sample, exists := byID[call.ID]; exists && sample.State == "complete" {
					copy := sample
					outcome.Sample = &copy
				}
			}
		}
		// A retired reclaimed terminal can be proven without a generation ID
		// on old call samples. Preserve its fact, never invent call partitioning.
		result.Turns[index].Outcomes = append(result.Turns[index].Outcomes, outcome)
	}
}

func contextInspectionHasUnknownTurns(turns []contextInspectionTurn) bool {
	for _, turn := range turns {
		if turn.Status == "unknown" {
			return true
		}
	}
	return false
}
