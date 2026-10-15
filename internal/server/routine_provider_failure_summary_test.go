package server

import (
	"reflect"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
)

func TestRoutineRunnerCycleSummaryRedactsOnlyHostProviderCheckpoints(t *testing.T) {
	const private = "PRIVATE_ROUTINE_PROVIDER_DETAIL"
	const legacy = `provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"` + private + `"}}`
	const safe = "The current model service rejected this request because of its subscription or billing status. The task, checkpoints, and existing results were preserved; renew or correct the current provider account, or choose another configured model before continuing."
	for _, test := range []struct {
		name, kind, detail string
		assistant          bool
	}{
		{"typed_subscription_checkpoint", "subscription", private, false},
		{"legacy_subscription_checkpoint", "", legacy, false},
		{"ordinary_assistant_text", "", legacy, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			journal := eventjournal.NewEventJournal(t.TempDir())
			message := eventjournal.Message{
				"type": "runner_checkpoint", "role": "system", "status": "running",
				"reasonCode":   sessionRunnerModelProviderUnavailableReasonCode,
				"resumeDetail": test.detail,
			}
			if test.kind != "" {
				message["provider_failure_kind"] = test.kind
			}
			if test.assistant {
				message = eventjournal.Message{"type": "message", "role": "assistant", "text": test.detail}
			}
			entry, err := journal.Append("routine-summary", message, eventjournal.Metadata{ClientMessageID: "summary-source"})
			if err != nil || entry == nil {
				t.Fatalf("append real private journal: entry=%#v err=%v", entry, err)
			}
			before, err := journal.ReadAfter("routine-summary", 0, 10)
			if err != nil || len(before) != 1 {
				t.Fatalf("read private source: entries=%#v err=%v", before, err)
			}
			cycle := SessionRunnerCycleResult{
				Claimed: true, SessionID: "routine-summary", Status: "interrupted", CheckpointEventID: entry.EventID,
			}
			want := safe
			if test.assistant {
				cycle.Status, cycle.AssistantEventID, cycle.CheckpointEventID = "completed", entry.EventID, 0
				want = test.detail
			}
			executor := &routineExecutor{server: &Server{eventJournal: journal}}
			summary, err := executor.runnerCycleSummary("routine-summary", cycle)
			if err != nil || summary != want {
				t.Errorf("public routine summary=%q err=%v want=%q", summary, err, want)
			}
			if !test.assistant {
				for _, forbidden := range []string{private, "InvalidSubscription", "provider endpoint returned 400"} {
					if strings.Contains(summary, forbidden) {
						t.Errorf("public routine outcome leaked private diagnostic %q", forbidden)
					}
				}
			}
			after, err := journal.ReadAfter("routine-summary", 0, 10)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("summary projection rewrote private journal: before=%#v after=%#v err=%v", before, after, err)
			}
		})
	}
}
