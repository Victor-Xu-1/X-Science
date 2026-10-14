package server

import (
	"encoding/json"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
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

// Follow the actual assistant/tool pairing sent to the provider. The raw
// provider response label is not the durable execution identity.
func requireProviderToolCallID(t *testing.T, request any, toolName string, ordinal int) string {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatCompletionRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, message := range decoded.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name != toolName {
				continue
			}
			if ordinal == 0 && call.ID != "" {
				return call.ID
			}
			ordinal--
		}
	}
	t.Fatalf("provider request has no assistant call for %s at remaining ordinal %d", toolName, ordinal)
	return ""
}
