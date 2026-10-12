package sessions

import (
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
)

// This deterministic store-only probe establishes the cancellation settlement
// boundary. It runs no provider, command, HTTP server, or scientific work.
func TestTaskRunCancellationInputInvestigationQueuesNormalInputBeforeSettlement(t *testing.T) {
	store := NewStore(t.TempDir())
	journal := eventjournal.NewEventJournal(store.root)
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.Upsert(Session{ID: "taskrun:input-boundary"}); err != nil {
		t.Fatal(err)
	}
	queued := appendCancellationInvestigationMessage(t, store, journal, "taskrun:input-boundary", "TaskRun Q")
	first, claimed, err := store.ClaimNextRunner("taskrun-worker", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	now = now.Add(time.Second)
	appendCancellationInvestigationMessage(t, store, journal, first.ID, "ordinary N")
	now = now.Add(time.Second)
	finish, err := journal.Append(first.ID, eventjournal.Message{"type": "runner_finished", "status": "cancelled"}, eventjournal.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	settled, _, err := store.FinalizeTaskRunCancelledRunner(FinalizeRunnerInput{
		SessionID: first.ID, RunnerID: first.Runner.RunnerID, Attempt: first.Runner.Attempt,
		Status: "cancelled", EventID: finish.EventID, Checkpoint: "TaskRun cancelled",
		CheckpointAt: now, FinishedAt: now,
	}, RunnerClaimFromSession(first), queued.EventID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewStore(store.root)
	restarted.now = store.now
	next, claimed, err := restarted.ClaimNextRunner("normal-input-worker", time.Minute)
	if err != nil || !claimed || next.ID != first.ID {
		t.Fatalf("new ordinary input is not schedulable after cancellation: settled=%#v next_claimed=%t err=%v", settled, claimed, err)
	}
}

func TestTaskRunCancellationInputInvestigationQueuesNormalInputAfterSettlementControl(t *testing.T) {
	store := NewStore(t.TempDir())
	journal := eventjournal.NewEventJournal(store.root)
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.Upsert(Session{ID: "taskrun:input-boundary"}); err != nil {
		t.Fatal(err)
	}
	queued := appendCancellationInvestigationMessage(t, store, journal, "taskrun:input-boundary", "TaskRun Q")
	first, claimed, err := store.ClaimNextRunner("taskrun-worker", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%t err=%v", claimed, err)
	}
	now = now.Add(time.Second)
	finish, err := journal.Append(first.ID, eventjournal.Message{"type": "runner_finished", "status": "cancelled"}, eventjournal.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizeTaskRunCancelledRunner(FinalizeRunnerInput{
		SessionID: first.ID, RunnerID: first.Runner.RunnerID, Attempt: first.Runner.Attempt,
		Status: "cancelled", EventID: finish.EventID, Checkpoint: "TaskRun cancelled",
		CheckpointAt: now, FinishedAt: now,
	}, RunnerClaimFromSession(first), queued.EventID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	appendCancellationInvestigationMessage(t, store, journal, first.ID, "ordinary N")
	next, claimed, err := store.ClaimNextRunner("normal-input-worker", time.Minute)
	if err != nil || !claimed || next.ID != first.ID {
		t.Fatalf("post-settlement ordinary input control failed: claimed=%t next=%#v err=%v", claimed, next, err)
	}
}

func appendCancellationInvestigationMessage(t *testing.T, store *Store, journal *eventjournal.EventJournal, sessionID, text string) *eventjournal.Entry {
	t.Helper()
	entry, err := journal.Append(sessionID, eventjournal.Message{"role": "user", "text": text}, eventjournal.Metadata{})
	if err != nil || entry == nil {
		t.Fatalf("journal append entry=%#v err=%v", entry, err)
	}
	if err := store.AppendMessageWithEventID(sessionID, "user", entry.EventID); err != nil {
		t.Fatal(err)
	}
	return entry
}
