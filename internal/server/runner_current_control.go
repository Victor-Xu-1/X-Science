package server

import (
	"fmt"

	eventjournal "x-science/internal/persistence/journal"
)

// sessionRunnerCurrentControl is a run-local projection of an already claimed,
// branch-validated input. It neither admits input nor replaces the durable
// scientific task intent. Only the existing control preparer constructs it.
type sessionRunnerCurrentControl struct {
	intent          string
	eventID         int64
	publicationSeq  int64
	clientMessageID string
}

func (control *sessionRunnerCurrentControl) instruction() string {
	if control != nil && control.intent == conversationSkillControlIntent {
		return conversationSkillContinuationInstruction
	}
	return ""
}

func (control *sessionRunnerCurrentControl) taskIdentity(streamUID string) (string, int64) {
	return fmt.Sprintf("%s:control:%d", streamUID, control.eventID), control.publicationSeq
}

// Replay intentionally hides every stored control bubble. The validated
// current action therefore needs its own continuation at the end of the
// assembled history, including when a compacted/recovered replay lacks that
// original input row. Rebuilding a request adds it once; it is never persisted
// as another user input or inferred from the raw localized control text.
func appendSessionRunnerCurrentControl(messages []chatCompletionMessage, control *sessionRunnerCurrentControl) []chatCompletionMessage {
	if instruction := control.instruction(); instruction != "" {
		return append(messages, chatCompletionMessage{Role: "user", Content: instruction})
	}
	return messages
}

// Current control work may review earlier scientific evidence, but that work
// does not inherit the earlier task's execution/delivery obligations. Keep the
// same canonical replay boundary across recovery and retain current receipts,
// input responses, and the trailing server-derived recovery projections. A
// compact boundary can omit the user row; event identity still bounds its
// retained checkpoints without manufacturing a second persistence authority.
func (control *sessionRunnerCurrentControl) scopeEntries(entries []eventjournal.Entry) ([]eventjournal.Entry, error) {
	filtered := make([]eventjournal.Entry, 0, len(entries))
	reached := false
	for _, entry := range entries {
		if entry.EventID > 0 {
			if entry.EventID < control.eventID {
				continue
			}
			reached = true
		} else if !reached {
			continue
		}
		filtered = append(filtered, entry)
	}
	return control.scopeContinuity(filtered)
}

// A current compact checkpoint can carry a continuity summary of earlier work.
// Filter only the read-side copy of the latest ledger. The original compact
// evidence and the normal decoder's validation remain unchanged.
func (control *sessionRunnerCurrentControl) scopeContinuity(entries []eventjournal.Entry) ([]eventjournal.Entry, error) {
	records, boundary, err := latestPersistedSessionRunnerToolContinuity(entries)
	if err != nil {
		return nil, err
	}
	if boundary <= 0 {
		return entries, nil
	}
	current := make([]sessionRunnerToolContinuityRecord, 0, len(records))
	for _, record := range records {
		if record.EventID >= control.eventID {
			current = append(current, record)
		}
	}
	for index := range entries {
		if entries[index].EventID != boundary {
			continue
		}
		message := make(eventjournal.Message, len(entries[index].Message))
		for key, value := range entries[index].Message {
			message[key] = value
		}
		message[sessionRunnerToolContinuityField] = current
		entries[index].Message = message
		break
	}
	return entries, nil
}
