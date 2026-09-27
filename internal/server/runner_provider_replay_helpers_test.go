package server

import (
	"testing"

	eventjournal "synon-go/internal/persistence/journal"
)

// All replay assertions use the production projection and its protocol checks.
func requireProviderReplayMessages(t *testing.T, system string, entries []eventjournal.Entry) []chatCompletionMessage {
	t.Helper()
	messages, err := sessionEntriesToProviderMessages(system, entries)
	if err != nil {
		t.Fatalf("provider replay: %v", err)
	}
	return messages
}
