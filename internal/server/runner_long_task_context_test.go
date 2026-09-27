package server

import (
	"strings"
	eventjournal "synon-go/internal/persistence/journal"
	"testing"
	"time"
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

func TestCommunicationScheduleRequestsUpdateAfterOneLongOperation(t *testing.T) {
	now := time.Now()
	schedule := &sessionRunnerCommunicationSchedule{now: func() time.Time { return now }}
	if err := schedule.published(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(20 * time.Minute)
	if schedule.due() {
		t.Fatal("idle time alone requested invented progress")
	}
	if err := schedule.settled(); err != nil {
		t.Fatal(err)
	}
	if !schedule.due() {
		t.Fatal("one long completed operation must not wait for three more operations to request an update")
	}
}
