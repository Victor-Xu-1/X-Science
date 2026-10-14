package workspace

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func continueWakeInput(t *testing.T, store *Store, key string) ResumeCompatibilityFrameInput {
	t.Helper()
	target, err := store.GetCompatibilityFrameResumeTarget(t.Context(), "wake-owner", "wake-frame")
	if err != nil {
		t.Fatal(err)
	}
	return ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{ActorID: "wake-owner", ClientCommandID: key, Target: target}}
}

func TestFrameContinueCommandAcceptedAndNoopRetriesPreserveNextWait(t *testing.T) {
	for _, noop := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted-wake", true: "accepted-noop"}[noop], func(t *testing.T) {
			store := newResumeWakeAuditStore(t)
			if !noop {
				parkResumeWakeAudit(t, store)
			}
			input := continueWakeInput(t, store, "153d9421-059f-4482-ad5f-1bc957ad6b14")
			first, err := store.ResumeCompatibilityFrameConversation("wake-frame", input)
			if err != nil || first.ContinueCommand == nil {
				t.Fatalf("first=%+v err=%v", first, err)
			}
			later := parkResumeWakeAudit(t, store)
			before, _ := json.Marshal(later.ResumeEvent.Payload)
			replay, err := store.ResumeCompatibilityFrameConversation("wake-frame", input)
			if err != nil || replay.ContinueCommand == nil || !replay.ContinueCommand.Replayed || replay.ContinueCommand.ID != first.ContinueCommand.ID || replay.NotifyDispatch {
				t.Fatalf("replay was rebound or scheduled against later wait: %+v %v", replay, err)
			}
			after, _, err := store.GetCompatibilityFrameResumeDispatch(later.ResumeEvent.ID)
			raw, _ := json.Marshal(after.ResumeEvent.Payload)
			if err != nil || string(before) != string(raw) {
				t.Fatalf("replay changed W2: before=%s after=%s err=%v", before, raw, err)
			}
			freshKeyOldTarget := input
			command := *input.Command
			command.ClientCommandID = "3d7c37fb-0709-40dc-b14f-d141d9e082bb"
			freshKeyOldTarget.Command = &command
			if _, err := store.ResumeCompatibilityFrameConversation("wake-frame", freshKeyOldTarget); !errors.Is(err, ErrFrameResumeTargetStale) {
				t.Fatalf("first-use stale W1 was accepted for W2: %v", err)
			}
		})
	}
}

func TestFrameContinueCommandConflictsAndBusyHaveNoEffects(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	input := continueWakeInput(t, store, "e6257f37-3754-4234-b6a4-7e5c88516837")
	if _, err := store.ResumeCompatibilityFrameConversation("wake-frame", input); err != nil {
		t.Fatal(err)
	}
	model := "changed-model"
	changed := input
	changed.Model = &model
	if _, err := store.ResumeCompatibilityFrameConversation("wake-frame", changed); !errors.Is(err, ErrFrameResumeCommandUsed) {
		t.Fatalf("changed controls accepted: %v", err)
	}
	foreign := input
	foreignCommand := *input.Command
	foreignCommand.ActorID = "foreign-owner"
	foreign.Command = &foreignCommand
	if _, err := store.ResumeCompatibilityFrameConversation("wake-frame", foreign); err == nil {
		t.Fatal("foreign owner accepted")
	}
	current := continueWakeInput(t, store, "3743809c-4b61-424c-b213-dc8cc7b28fb0")
	claim, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("controlled-busy", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	var before string
	if err := store.db.QueryRow(`SELECT payload FROM frame_events WHERE id=?`, claim.ResumeEvent.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResumeCompatibilityFrameConversation("wake-frame", current); !errors.Is(err, ErrFrameResumeCommandBusy) {
		t.Fatalf("claim was not explicit busy: %v", err)
	}
	var after string
	if err := store.db.QueryRow(`SELECT payload FROM frame_events WHERE id=?`, claim.ResumeEvent.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&count); err != nil || count != 1 || after != before {
		t.Fatalf("rejected command had effects: count=%d err=%v", count, err)
	}
}

func TestFrameContinueCommandAuditFailureRollsBackResumeAndPresentation(t *testing.T) {
	store := newResumeDescriptionStore(t)
	const frameID = "command-rollback"
	if _, err := store.CreateFrame(CreateFrameInput{ID: frameID, ProjectID: "project-resume-description", AgentName: "OPERON", Status: "failed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,status_description,context_data) VALUES(?,'preserve controlled failure','{}')`, frameID); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetCompatibilityFrameResumeTarget(t.Context(), "owner-resume-description", frameID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_continue_command BEFORE INSERT ON frame_events WHEN NEW.event_type='frame_resume_continue_command_accepted' BEGIN SELECT RAISE(ABORT,'controlled command failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResumeCompatibilityFrameConversation(frameID, ResumeCompatibilityFrameInput{Command: &CompatibilityFrameContinueCommand{ActorID: "owner-resume-description", ClientCommandID: "b81198cc-810a-4d70-bd96-038467a3a161", Target: target}}); err == nil {
		t.Fatal("failed audit committed resume")
	}
	assertResumeDescription(t, store, frameID, "failed", "preserve controlled failure")
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=?`, frameID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left events=%d err=%v", count, err)
	}
}

func TestFrameContinueCommandConcurrentRetriesHaveOneAcceptedResult(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	parkResumeWakeAudit(t, store)
	input := continueWakeInput(t, store, "a7f900bd-503f-4fc1-b2d4-968edb97fb69")
	type response struct {
		result ResumeCompatibilityFrameResult
		err    error
	}
	results := make(chan response, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Go(func() {
			<-start
			result, err := store.ResumeCompatibilityFrameConversation("wake-frame", input)
			results <- response{result, err}
		})
	}
	close(start)
	group.Wait()
	close(results)
	id, replays := "", 0
	for response := range results {
		if response.err != nil || response.result.ContinueCommand == nil {
			t.Fatalf("result=%+v err=%v", response.result, response.err)
		}
		if id != "" && response.result.ContinueCommand.ID != id {
			t.Fatal("same command created different receipts")
		}
		id = response.result.ContinueCommand.ID
		if response.result.ContinueCommand.Replayed {
			replays++
		}
	}
	if replays != 1 {
		t.Fatalf("replays=%d want one", replays)
	}
	var commands, wakes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_dispatch_woken'`).Scan(&wakes); err != nil || commands != 1 || wakes != 1 {
		t.Fatalf("commands=%d wakes=%d err=%v", commands, wakes, err)
	}
}
