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
	ids := providerToolCallIDs(t, request, toolName)
	if ordinal < 0 || ordinal >= len(ids) {
		t.Fatalf("provider request has %d assistant calls for %s, want ordinal %d", len(ids), toolName, ordinal)
	}
	return ids[ordinal]
}

func requireLatestProviderToolCallID(t *testing.T, request any, toolName string) string {
	t.Helper()
	ids := providerToolCallIDs(t, request, toolName)
	if len(ids) == 0 {
		t.Fatalf("provider request has no assistant call for %s", toolName)
	}
	return ids[len(ids)-1]
}

func providerToolCallIDs(t *testing.T, request any, toolName string) []string {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded chatCompletionRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var ids []string
	seen := map[string]bool{}
	for _, message := range decoded.Messages {
		if message.Role != "assistant" {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.Function.Name != toolName {
				continue
			}
			if call.ID == "" || seen[call.ID] {
				t.Fatalf("provider assistant call for %s has missing or duplicate identity %q", toolName, call.ID)
			}
			seen[call.ID] = true
			ids = append(ids, call.ID)
		}
	}
	return ids
}
