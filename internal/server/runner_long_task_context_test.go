package server

import (
	"strings"
	eventjournal "synon-go/internal/persistence/journal"
	"testing"
)

func TestProviderReplayPreservesExactUserConstraintsAfterCompaction(t *testing.T) {
	first := "Initial requirements: " + strings.Repeat("preserve inputs; ", 250) + "TAIL_REQUIREMENT"
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": first}},
		{EventID: 2, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Also retain failure evidence."}},
		{EventID: 3, Message: eventjournal.Message{"type": "runner_checkpoint", "toolPhase": "auto_compact", "status": "completed", "summary": "Only recent work."}},
		{EventID: 4, Message: eventjournal.Message{"type": "message", "role": "user", "text": "Continue."}},
	}
	messages, err := sessionEntriesToProviderMessages("policy", entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{first, "Also retain failure evidence.", "Continue."} {
		count := 0
		for _, message := range messages {
			if message.Role == "user" && message.Content == want {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("user constraint not preserved exactly once: count=%d tail=%q", count, want[len(want)-10:])
		}
	}
}
