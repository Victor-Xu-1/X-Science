package server

import (
	"fmt"
	"strings"

	eventjournal "synon-go/internal/persistence/journal"
)

// compactDurableRuntimeContextLines projects the bounded server-derived tool
// ledger into both deterministic and model-generated compaction inputs. This
// keeps exact failures, accepted outputs, workspace paths, and unresolved work
// available even when an optional summarizer times out. Immutable checkpoints
// remain the evidence authority; this is only their compact continuation view.
func compactDurableRuntimeContextLines(entries []eventjournal.Entry) ([]string, error) {
	records, err := sessionRunnerToolContinuityRecords(entries)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	lines := []string{"Durable execution and repair state:"}
	lines = append(lines, strings.Split(sessionRunnerToolContinuityContext(records), "\n")...)
	latest := records[len(records)-1]
	if !latest.Successful {
		lines = append(lines, fmt.Sprintf(
			"Pending work: reconcile tool call %s (%s) from event %d using its immutable receipt and exact failureDiagnostic. Preserve usable partial and prior results; repair or replace only the unresolved step.",
			latest.ToolCallID, latest.ToolName, latest.EventID,
		))
	} else {
		lines = append(lines, fmt.Sprintf(
			"Continuation status: tool call %s (%s) returned a successful result at event %d. This applies only to that call's recorded outcome and does not establish task completion. Compare its execution provenance and outputs with the remaining task requirements, reuse valid receipts, and continue unfinished work.",
			latest.ToolCallID, latest.ToolName, latest.EventID,
		))
	}
	return lines, nil
}
