package sessions

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	eventjournal "x-science/internal/persistence/journal"
)

func TestStoreLastUserEventIDPersistsExactlyAndIsOptional(t *testing.T) {
	store := NewStore(t.TempDir())
	const eventID int64 = 9007199254740993 // Must never round through float64.
	if err := store.Save(Session{ID: "taskrun:exact-id", LastUserEventID: eventID}); err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(store.root)
	session, found, err := reopened.Get("taskrun:exact-id")
	if err != nil || !found || session.LastUserEventID != eventID {
		t.Fatalf("reopened session=%#v found=%t err=%v", session, found, err)
	}
	if err := reopened.Save(Session{ID: session.ID}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(reopened.path)
	if err != nil || bytes.Contains(data, []byte("lastUserEventId")) {
		t.Fatalf("zero watermark must be omitted: data=%s err=%v", data, err)
	}
}

func TestStoreLastUserEventIDProjectionNeverRegresses(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	const id = "taskrun:monotonic"
	if err := store.Upsert(Session{ID: id}); err != nil {
		t.Fatal(err)
	}
	for _, eventID := range []int64{10, 10, 4, 17, 12} {
		if err := store.AppendMessageWithEventID(id, "user", eventID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendMessageWithEventID(id, "assistant", 100); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(id, "user"); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{3, 7, 8} {
		if err := store.ReconcileMessageStats(id, count, "assistant", now, 2); err != nil {
			t.Fatal(err)
		}
	}
	session, _, err := store.Get(id)
	if err != nil || session.LastUserEventID != 17 || session.MessageCount != 8 {
		t.Fatalf("projection session=%#v err=%v", session, err)
	}
	if err := store.ReconcileMessageStats(id, 3, "user", now.Add(-time.Minute), 20); err != nil {
		t.Fatal(err)
	}
	session, _, err = store.Get(id)
	if err != nil || session.LastUserEventID != 20 || session.MessageCount != 8 || session.LastRole != "assistant" {
		t.Fatalf("late watermark must not replace newer stats: session=%#v err=%v", session, err)
	}
	if err := store.Upsert(Session{ID: id, LastUserEventID: 1}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.SaveProjectionPreservingRunner(Session{ID: id, MessageCount: 8, LastRole: "assistant", LastUserEventID: 2})
	if err != nil || projection.LastUserEventID != 20 {
		t.Fatalf("compatibility projection=%#v err=%v", projection, err)
	}
	// Save is the authoritative history replacement path, including zero.
	for _, eventID := range []int64{5, 0} {
		projection.LastUserEventID = eventID
		if err := store.Save(projection); err != nil {
			t.Fatal(err)
		}
		session, _, err = store.Get(id)
		if err != nil || session.LastUserEventID != eventID {
			t.Fatalf("authoritative rewrite session=%#v err=%v", session, err)
		}
	}
}

func TestStoreTaskRunCancelledFinalizationPreservesMultipleNewerInputs(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:multiple", 1)
	for _, eventID := range []int64{2, 3} {
		*now = now.Add(time.Second)
		if err := store.AppendMessageWithEventID(first.ID, "user", eventID); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(time.Second)
	if err := store.AppendMessageWithEventID(first.ID, "assistant", 4); err != nil {
		t.Fatal(err)
	}
	before, _, err := store.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	input := cancellationFinish(first, *now, 5)
	settled, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), 1)
	if err != nil || !applied || settled.Runner != nil || settled.LastRole != "user" || settled.LastUserEventID != 3 ||
		settled.MessageCount != before.MessageCount || !settled.LastUserMessageAt.Equal(before.LastUserMessageAt) {
		t.Fatalf("cancelled settlement=%#v applied=%t err=%v before=%#v", settled, applied, err, before)
	}
	reopened := NewStore(store.root)
	reopened.now = store.now
	next, claimed, err := reopened.ClaimNextRunner("ordinary-worker", time.Minute)
	if err != nil || !claimed || next.ID != first.ID || next.LastUserEventID != 3 {
		t.Fatalf("pending input did not survive restart: next=%#v claimed=%t err=%v", next, claimed, err)
	}
	// A repeated old finalizer must not remove the newly claimed ordinary work.
	if _, applied, err := reopened.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), 1); !errors.Is(err, ErrRunnerClaimStale) || applied {
		t.Fatalf("old finalization after requeue applied=%t err=%v", applied, err)
	}
	current, _, err := reopened.Get(first.ID)
	if err != nil || current.Runner == nil || current.Runner.RunnerID != "ordinary-worker" || current.Runner.Status != "running" {
		t.Fatalf("old finalization changed ordinary claim: session=%#v err=%v", current, err)
	}
}

func TestStoreTaskRunCancelledWithoutNewerInputStaysTerminal(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:no-newer-input", 1)
	*now = now.Add(time.Second)
	input := cancellationFinish(first, *now, 2)
	settled, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), 1)
	if err != nil || !applied || settled.Runner == nil || settled.Runner.Status != "cancelled" || settled.LastUserEventID != 1 {
		t.Fatalf("terminal settlement=%#v applied=%t err=%v", settled, applied, err)
	}
	if _, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), 1); err != nil || applied {
		t.Fatalf("repeated terminal cancellation applied=%t err=%v", applied, err)
	}
	if next, claimed, err := store.ClaimNextRunner("ordinary-worker", time.Minute); err != nil || claimed {
		t.Fatalf("no newer input was queued: next=%#v claimed=%t err=%v", next, claimed, err)
	}
	*now = now.Add(time.Second)
	if err := store.AppendMessageWithEventID(first.ID, "user", 3); err != nil {
		t.Fatal(err)
	}
	next, claimed, err := store.ClaimNextRunner("ordinary-worker", time.Minute)
	if err != nil || !claimed || next.ID != first.ID {
		t.Fatalf("post-finalization input not queued: next=%#v claimed=%t err=%v", next, claimed, err)
	}
}

func TestStoreOrdinaryFinalizeRunnerSemanticsUnchanged(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			store, now, first := cancellationInputClaim(t, "ordinary:"+status, 1)
			*now = now.Add(time.Second)
			if err := store.AppendMessageWithEventID(first.ID, "user", 2); err != nil {
				t.Fatal(err)
			}
			if err := store.AppendMessage(first.ID, "assistant"); err != nil {
				t.Fatal(err)
			}
			input := cancellationFinish(first, *now, 3)
			input.Status = status
			settled, applied, err := store.FinalizeRunner(input)
			if err != nil || !applied || settled.Runner == nil || settled.Runner.Status != status || settled.LastRole != "assistant" {
				t.Fatalf("ordinary settlement=%#v applied=%t err=%v", settled, applied, err)
			}
		})
	}
}

func TestStoreTaskRunCancellationFencesSameRunnerReclaimAndRewind(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:rewind", 2)
	oldClaim := RunnerClaimFromSession(first)
	*now = now.Add(time.Second)
	// Default history rewind explicitly recomputes the watermark and resets the
	// runner. The old cancellation loses authority even before another claim.
	rewritten := first
	rewritten.Runner = nil
	rewritten.LastUserEventID = 1
	if err := store.Save(rewritten); err != nil {
		t.Fatal(err)
	}
	input := cancellationFinish(first, *now, 3)
	if _, applied, err := store.FinalizeTaskRunCancelledRunner(input, oldClaim, 2); !errors.Is(err, ErrRunnerClaimStale) || applied {
		t.Fatalf("rewind did not invalidate finalizer: applied=%t err=%v", applied, err)
	}
	second, claimed, err := store.ClaimRunner(first.ID, first.Runner.RunnerID, time.Minute)
	if err != nil || !claimed || second.Runner.Attempt != first.Runner.Attempt || RunnerClaimFromSession(second).ClaimToken == oldClaim.ClaimToken {
		t.Fatalf("same-name new claim=%#v claimed=%t err=%v", second, claimed, err)
	}
	if _, applied, err := store.FinalizeTaskRunCancelledRunner(input, oldClaim, 2); !errors.Is(err, ErrRunnerClaimStale) || applied {
		t.Fatalf("same runner/attempt token fence failed: applied=%t err=%v", applied, err)
	}
	current, _, err := store.Get(first.ID)
	if err != nil || current.LastUserEventID != 1 || current.Runner == nil || current.Runner.Status != "running" {
		t.Fatalf("stale finalizer mutated replacement=%#v err=%v", current, err)
	}
}

func TestStoreHydrateLastUserEventIDUsesObservedInputCAS(t *testing.T) {
	for _, changedField := range []string{"count", "role", "input time", "watermark"} {
		t.Run(changedField, func(t *testing.T) {
			store, _, first := cancellationInputClaim(t, "taskrun:hydrate-cas", 0)
			changed := first
			switch changedField {
			case "count":
				changed.MessageCount++
			case "role":
				changed.LastRole = "assistant"
			case "input time":
				changed.LastUserMessageAt = changed.LastUserMessageAt.Add(time.Second)
			case "watermark":
				changed.LastUserEventID = 9
			}
			if err := store.Save(changed); err != nil {
				t.Fatal(err)
			}
			current, accepted, err := store.HydrateLastUserEventID(RunnerClaimFromSession(first), first, 1)
			if err != nil || accepted || current.LastUserEventID != changed.LastUserEventID {
				t.Fatalf("changed input accepted: current=%#v accepted=%t err=%v", current, accepted, err)
			}
		})
	}
}

func TestStoreHydrateLastUserEventIDStrictReadRetryAndRestart(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:legacy-hydration", 0)
	journal := eventjournal.NewEventJournal(store.root)
	if _, err := journal.Append(first.ID, eventjournal.Message{"role": "user", "text": "Q"}, eventjournal.Metadata{}); err != nil {
		t.Fatal(err)
	}
	// Strict journal I/O is completed outside the sessions lock. A projection
	// arriving between read and CAS requires another strict snapshot.
	entries, err := journal.ReadAllStrict(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	normal := appendCancellationInvestigationMessage(t, store, journal, first.ID, "N")
	current, accepted, err := store.HydrateLastUserEventID(RunnerClaimFromSession(first), first, entries[len(entries)-1].EventID)
	if err != nil || accepted || current.LastUserEventID != normal.EventID {
		t.Fatalf("stale read hydration=%#v accepted=%t err=%v", current, accepted, err)
	}
	entries, err = journal.ReadAllStrict(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	hydrated, accepted, err := store.HydrateLastUserEventID(RunnerClaimFromSession(first), current, entries[len(entries)-1].EventID)
	if err != nil || !accepted || hydrated.LastUserEventID != normal.EventID {
		t.Fatalf("retry hydration=%#v accepted=%t err=%v", hydrated, accepted, err)
	}
	// A zero-watermark legacy session can also be hydrated after restart.
	legacy := hydrated
	legacy.LastUserEventID = 0
	if err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(store.root)
	reopened.now = store.now
	observed, _, err := reopened.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	hydrated, accepted, err = reopened.HydrateLastUserEventID(RunnerClaimFromSession(first), observed, normal.EventID)
	if err != nil || !accepted || hydrated.LastUserEventID != normal.EventID || hydrated.MessageCount != observed.MessageCount ||
		!hydrated.LastUserMessageAt.Equal(observed.LastUserMessageAt) || !hydrated.UpdatedAt.Equal(observed.UpdatedAt) {
		t.Fatalf("restart hydration=%#v accepted=%t err=%v", hydrated, accepted, err)
	}
	wrongClaim := RunnerClaimFromSession(first)
	wrongClaim.ClaimToken = "stale-token"
	if _, accepted, err := reopened.HydrateLastUserEventID(wrongClaim, hydrated, 99); !errors.Is(err, ErrRunnerClaimStale) || accepted {
		t.Fatalf("stale hydration claim accepted=%t err=%v", accepted, err)
	}
	*now = now.Add(2 * time.Minute)
	if _, accepted, err := reopened.HydrateLastUserEventID(RunnerClaimFromSession(first), hydrated, 99); !errors.Is(err, ErrRunnerClaimStale) || accepted {
		t.Fatalf("expired hydration claim accepted=%t err=%v", accepted, err)
	}
}

func TestStoreRealJournalProjectionFailureCanRetry(t *testing.T) {
	store, _, first := cancellationInputClaim(t, "taskrun:projection-retry", 0)
	journal := eventjournal.NewEventJournal(store.root)
	entry, err := journal.Append(first.ID, eventjournal.Message{"role": "user", "text": "accepted after projection"}, eventjournal.Metadata{})
	if err != nil || entry == nil {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageWithEventID(first.ID, "user", entry.EventID); err == nil {
		t.Fatal("failed durable projection was reported as accepted")
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed projection changed durable state: err=%v", err)
	}
	if err := os.Remove(store.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageWithEventID(first.ID, "user", entry.EventID); err != nil {
		t.Fatal(err)
	}
	current, _, err := store.Get(first.ID)
	if err != nil || current.LastUserEventID != entry.EventID || current.MessageCount != first.MessageCount+1 {
		t.Fatalf("retry projection=%#v err=%v", current, err)
	}
}

func TestStoreTaskRunCancelledFinalizationFailureCanRetryWithoutChangingJournal(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:finalize-retry", 0)
	journal := eventjournal.NewEventJournal(store.root)
	queued := appendCancellationInvestigationMessage(t, store, journal, first.ID, "Q")
	*now = now.Add(time.Second)
	appendCancellationInvestigationMessage(t, store, journal, first.ID, "N")
	finish, err := journal.Append(first.ID, eventjournal.Message{"type": "runner_finished", "status": "cancelled"}, eventjournal.Metadata{})
	if err != nil || finish == nil {
		t.Fatalf("finish=%#v err=%v", finish, err)
	}
	journalBefore, err := journal.ReadAllStrict(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	input := cancellationFinish(first, *now, finish.EventID)
	if _, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), queued.EventID); err == nil || applied {
		t.Fatalf("failed finalization applied=%t err=%v", applied, err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed finalization changed durable projection: err=%v", err)
	}
	if err := os.Remove(store.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	settled, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), queued.EventID)
	if err != nil || !applied || settled.Runner != nil || settled.LastRole != "user" {
		t.Fatalf("retry finalization=%#v applied=%t err=%v", settled, applied, err)
	}
	journalAfter, err := journal.ReadAllStrict(first.ID)
	if err != nil || len(journalAfter) != len(journalBefore) || journalAfter[len(journalAfter)-1].EventID != finish.EventID ||
		journalAfter[len(journalAfter)-1].Message["status"] != "cancelled" {
		t.Fatalf("cancelled finish history changed: entries=%#v err=%v", journalAfter, err)
	}
}

func TestStoreTaskRunCancellationRejectsInvalidProjectionAndOptions(t *testing.T) {
	store, now, first := cancellationInputClaim(t, "taskrun:invalid-options", 1)
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventID := range []int64{0, -1} {
		if err := store.AppendMessageWithEventID(first.ID, "user", eventID); err == nil {
			t.Fatalf("invalid event ID %d accepted", eventID)
		}
	}
	if err := store.ReconcileMessageStats(first.ID, 1, "user", *now, -1); err == nil {
		t.Fatal("negative reconcile watermark accepted")
	}
	if err := store.ReconcileMessageStats(first.ID, 1, "user", *now, 1, 2); err == nil {
		t.Fatal("ambiguous reconcile watermark accepted")
	}
	input := cancellationFinish(first, *now, 2)
	for _, status := range []string{"completed", "failed", "running"} {
		invalid := input
		invalid.Status = status
		if _, applied, err := store.FinalizeTaskRunCancelledRunner(invalid, RunnerClaimFromSession(first), 1); err == nil || applied {
			t.Fatalf("invalid cancellation status %s applied=%t err=%v", status, applied, err)
		}
	}
	if _, applied, err := store.FinalizeTaskRunCancelledRunner(input, RunnerClaimFromSession(first), 0); err == nil || applied {
		t.Fatalf("unknown consumed ID applied=%t err=%v", applied, err)
	}
	if _, accepted, err := store.HydrateLastUserEventID(RunnerClaimFromSession(first), first, -1); err == nil || accepted {
		t.Fatalf("negative hydration ID accepted=%t err=%v", accepted, err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("invalid inputs changed durable state: err=%v", err)
	}
}

func cancellationInputClaim(t *testing.T, id string, eventID int64) (*Store, *time.Time, Session) {
	t.Helper()
	store := NewStore(t.TempDir())
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.Save(Session{ID: id, LastRole: "user", LastUserEventID: eventID, MessageCount: 1, LastUserMessageAt: now}); err != nil {
		t.Fatal(err)
	}
	first, claimed, err := store.ClaimNextRunner("taskrun-worker", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim=%#v claimed=%t err=%v", first, claimed, err)
	}
	return store, &now, first
}

func cancellationFinish(first Session, now time.Time, eventID int64) FinalizeRunnerInput {
	return FinalizeRunnerInput{
		SessionID: first.ID, RunnerID: first.Runner.RunnerID, Attempt: first.Runner.Attempt,
		Status: "cancelled", EventID: eventID, Checkpoint: "TaskRun cancelled", CheckpointAt: now, FinishedAt: now,
	}
}
