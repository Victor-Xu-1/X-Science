package workspace

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameResumeWakeAuditTaskSettlementRemainsAtomic(t *testing.T) {
	store, operation := taskOperationFixture(t)
	ctx := context.Background()
	if _, err := store.EnqueueTaskOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutbox(ctx, ClaimOutboxInput{WorkerID: "wake-settlement", Topics: []string{TaskOperationOutboxTopic}, Limit: 1, Lease: time.Minute})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	resume, err := store.CreateAutoResumeDispatch(operation.FrameID, operation.RootFrameID, "project", "OPERON", "test_recovery")
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("wake-dispatch", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%#v found=%t err=%v", claim, found, err)
	}
	if _, _, err := store.RequeueCompatibilityFrameResumeDispatch(RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: resume.Event.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
		ReasonCode: "test_recovery", NotBefore: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_settlement_wake BEFORE INSERT ON frame_events WHEN NEW.event_type='frame_resume_dispatch_woken' BEGIN SELECT RAISE(ABORT,'controlled wake audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleTaskOperation(ctx, claimed[0], map[string]any{"status": "completed"}); err == nil {
		t.Fatal("failed wake audit committed settlement")
	}
	current, err := store.GetOutboxEvent(ctx, claimed[0].ID)
	if err != nil || current.Status != OutboxStatusInflight {
		t.Fatalf("settlement lost claim: %#v %v", current, err)
	}
	parked, _, err := store.GetCompatibilityFrameResumeDispatch(resume.Event.ID)
	if err != nil || parked.NotBefore.IsZero() {
		t.Fatalf("settlement cleared fence: %#v %v", parked, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_settlement_wake`); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleTaskOperation(ctx, claimed[0], map[string]any{"status": "completed"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := store.db.QueryRow(`SELECT payload FROM frame_events WHERE event_type='frame_resume_dispatch_woken' AND frame_id=?`, operation.FrameID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		Source  string               `json:"source"`
		WaitRef string               `json:"waitRef"`
		Cause   FrameResumeWakeCause `json:"cause"`
	}
	if err := json.Unmarshal([]byte(raw), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Source != string(FrameResumeWakeTaskOperation) || audit.WaitRef != parked.WaitRef || audit.Cause.ReferenceID != claimed[0].ID || audit.Cause.OperationID != operation.ID || audit.Cause.NotificationID != operation.NotificationID || audit.Cause.Evidence != "canonical" {
		t.Fatalf("settlement witness=%#v", audit)
	}
}

func TestFrameResumeWakeAuditModelAndGenericShareOnlyOneWaitWinner(t *testing.T) {
	for _, modelFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "model-first", false: "generic-first"}[modelFirst], func(t *testing.T) {
			store := newResumeWakeAuditStore(t)
			wait := parkResumeWakeAudit(t, store)
			var first FrameEvent
			if modelFirst {
				var state CompatibilityFrameResumeDispatchWakeState
				var err error
				first, state, err = store.SignalCompatibilityFrameResumeDispatchModelSwitch(wait.FrameID, 7, "provider_failure")
				if err != nil || state != CompatibilityFrameResumeDispatchWakeWoken {
					t.Fatal(err)
				}
				duplicate, changed, err := store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(wait, "later-continue"))
				if err != nil || changed || duplicate.ID != first.ID || duplicate.Payload["source"] != string(FrameResumeWakeModelSwitch) {
					t.Fatalf("generic replaced model winner: %#v %t %v", duplicate, changed, err)
				}
			} else {
				var changed bool
				var err error
				first, changed, err = store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(wait, "first-continue"))
				if err != nil || !changed {
					t.Fatal(err)
				}
			}
			before, _, _ := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
			if _, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(wait.FrameID, 8, "provider_failure"); err != nil || state != CompatibilityFrameResumeDispatchWakePending {
				t.Fatalf("duplicate model state=%s err=%v", state, err)
			}
			after, _, _ := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
			if before.ResumeEvent.Payload["dispatch"].(map[string]any)["wokenAt"] != after.ResumeEvent.Payload["dispatch"].(map[string]any)["wokenAt"] {
				t.Fatal("new model revision rewrote wake time without a new wait")
			}
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_dispatch_woken'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("wake count=%d err=%v", count, err)
			}
			if first.ID == "" {
				t.Fatal("missing wake winner")
			}
		})
	}
}

func resumeWakeCommand(wait CompatibilityFrameResumeDispatch, command string) FrameResumeDispatchWakeInput {
	return FrameResumeDispatchWakeInput{ResumeEventID: wait.ResumeEvent.ID, ExpectedWaitRef: wait.WaitRef,
		Source: FrameResumeWakeContinue, ActorID: "wake-owner",
		Cause: FrameResumeWakeCause{Kind: "continue_command", ReferenceID: command, Evidence: "observed"}}
}

func TestFrameResumeWakeAuditArmedModelSwitchRetainsItsOwnRevisionPath(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	claim, claimed, err := store.ClaimNextCompatibilityFrameResumeDispatch("armed-model", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%#v claimed=%t err=%v", claim, claimed, err)
	}
	for _, revision := range []int64{7, 8} {
		event, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(claim.FrameID, revision, "provider_failure")
		if err != nil || state != CompatibilityFrameResumeDispatchWakeArmed || event.Payload["claimEventId"] == "" || event.Payload["waitRef"] != nil {
			t.Fatalf("armed event=%#v state=%s err=%v", event, state, err)
		}
	}
	event, resumed, err := store.FailCompatibilityFrameResumeDispatch(claim.ResumeEvent.ID, claim.Attempt, claim.ClaimToken, "provider_failure")
	if err != nil || !resumed || event.Payload["source"] != string(FrameResumeWakeModelSwitch) {
		t.Fatalf("consume event=%#v resumed=%t err=%v", event, resumed, err)
	}
	cause, ok := event.Payload["cause"].(FrameResumeWakeCause)
	if !ok || cause.Revision != 8 || cause.Evidence != "observed" {
		t.Fatalf("consumed wrong revision: %#v", event)
	}
	var armed, refs int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_dispatch_model_switch_armed'`).Scan(&armed); err != nil || armed != 2 {
		t.Fatalf("armed=%d err=%v", armed, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE id=? AND event_type='frame_resume_dispatch_model_switch_armed'`, event.Payload["armedEventId"]).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("consumed armed ref=%d err=%v", refs, err)
	}
}

func TestFrameResumeWakeAuditRejectsForeignContinueActorAtomically(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	wait := parkResumeWakeAudit(t, store)
	input := resumeWakeCommand(wait, "foreign-command")
	input.ActorID = "other-owner"
	if _, _, err := store.WakeCompatibilityFrameResumeDispatch(input); err == nil {
		t.Fatal("foreign actor was recorded as an accepted command")
	}
	current, _, _ := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
	if current.NotBefore.IsZero() {
		t.Fatal("rejected actor cleared the fence")
	}
}

func TestFrameResumeWakeAuditBindsWinnerAndRejectsStaleWait(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	first := parkResumeWakeAudit(t, store)
	input := resumeWakeCommand(first, "command-one")
	a, changed, err := store.WakeCompatibilityFrameResumeDispatch(input)
	if err != nil || !changed {
		t.Fatalf("wake=%#v changed=%t err=%v", a, changed, err)
	}
	replay := input
	replay.Source, replay.ActorID = FrameResumeWakeKernelOperation, ""
	replay.Cause = FrameResumeWakeCause{Kind: "kernel_operation_version", ReferenceID: "operation", Evidence: "observed", OperationVersion: 3}
	b, changed, err := store.WakeCompatibilityFrameResumeDispatch(replay)
	if err != nil || changed || b.ID != a.ID || !b.CreatedAt.Equal(a.CreatedAt) || b.Payload["source"] != string(FrameResumeWakeContinue) {
		t.Fatalf("retry changed winner: first=%#v replay=%#v changed=%t err=%v", a, b, changed, err)
	}
	second := parkResumeWakeAudit(t, store)
	if second.WaitRef == first.WaitRef {
		t.Fatal("new wait reused previous reference")
	}
	if _, changed, err := store.WakeCompatibilityFrameResumeDispatch(input); err != nil || changed {
		t.Fatalf("stale wait changed=%t err=%v", changed, err)
	}
	current, _, _ := store.GetCompatibilityFrameResumeDispatch(second.ResumeEvent.ID)
	if current.NotBefore.IsZero() || current.WaitRef != second.WaitRef {
		t.Fatal("stale wake cleared the current fence")
	}
	if _, _, err := store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(second, "command-one")); err == nil {
		t.Fatal("reused Continue command was rebound to another wait")
	}
	if _, changed, err := store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(second, "command-two")); err != nil || !changed {
		t.Fatalf("new wait wake changed=%t err=%v", changed, err)
	}
}

func TestFrameResumeWakeAuditConcurrentCausesHaveOneWinner(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	wait := parkResumeWakeAudit(t, store)
	type result struct {
		event   FrameEvent
		changed bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, command := range []string{"one", "two"} {
		wg.Add(1)
		go func(command string) {
			defer wg.Done()
			<-start
			e, c, err := store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(wait, command))
			results <- result{e, c, err}
		}(command)
	}
	close(start)
	wg.Wait()
	close(results)
	winners, id := 0, ""
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.changed {
			winners++
		}
		if id != "" && result.event.ID != id {
			t.Fatal("concurrent causes received different audit winners")
		}
		id = result.event.ID
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
	var commands int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&commands); err != nil || commands != 1 {
		t.Fatalf("commands=%d err=%v", commands, err)
	}
}

func TestFrameResumeWakeAuditFailureRollsBackFenceAndCause(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	wait := parkResumeWakeAudit(t, store)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_wake_audit BEFORE INSERT ON frame_events WHEN NEW.event_type='frame_resume_dispatch_woken' BEGIN SELECT RAISE(ABORT,'controlled audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.WakeCompatibilityFrameResumeDispatch(resumeWakeCommand(wait, "rollback")); err == nil {
		t.Fatal("failed audit committed a wake")
	}
	current, _, err := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
	if err != nil || current.NotBefore.IsZero() || current.WaitRef != wait.WaitRef {
		t.Fatalf("rollback lost fence: %#v %v", current, err)
	}
	var commands int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_resume_continue_command_accepted'`).Scan(&commands); err != nil || commands != 0 {
		t.Fatalf("rollback commands=%d err=%v", commands, err)
	}
}

func TestFrameResumeWakeAuditLegacyWaitIsExplicitlyUnknown(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	wait := parkResumeWakeAudit(t, store)
	if _, err := store.db.Exec(`UPDATE frame_events SET payload=json_remove(payload,'$.dispatch.waitEventId') WHERE id=?`, wait.ResumeEvent.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _, _ := store.GetCompatibilityFrameResumeDispatch(wait.ResumeEvent.ID)
	if !strings.HasPrefix(legacy.WaitRef, "legacy-wait:") {
		t.Fatalf("legacy reference=%q", legacy.WaitRef)
	}
	event, changed, err := wakeCompatibilityDispatchForTest(store, legacy.ResumeEvent.ID)
	if err != nil || !changed || event.Payload["source"] != string(FrameResumeWakeLegacyUnknown) {
		t.Fatalf("legacy wake=%#v changed=%t err=%v", event, changed, err)
	}
	cause, ok := event.Payload["cause"].(FrameResumeWakeCause)
	if !ok || cause.Evidence != "unknown" || cause.ReferenceID != "" || event.Payload["waitRef"] != legacy.WaitRef {
		t.Fatalf("invented legacy cause: %#v", event)
	}
}

func TestFrameResumeWakeAuditModelSwitchKeepsObservedEvidencePerWait(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	first := parkResumeWakeAudit(t, store)
	a, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(first.FrameID, 7, "provider_failure")
	if err != nil || state != CompatibilityFrameResumeDispatchWakeWoken {
		t.Fatalf("first model wake=%#v state=%s err=%v", a, state, err)
	}
	second := parkResumeWakeAudit(t, store)
	b, state, err := store.SignalCompatibilityFrameResumeDispatchModelSwitch(second.FrameID, 7, "provider_failure")
	if err != nil || state != CompatibilityFrameResumeDispatchWakeWoken || b.ID == a.ID || b.Payload["waitRef"] != second.WaitRef {
		t.Fatalf("second model wake=%#v state=%s err=%v", b, state, err)
	}
	cause, ok := b.Payload["cause"].(FrameResumeWakeCause)
	if !ok || cause.Evidence != "observed" || cause.Revision != 7 {
		t.Fatalf("model revision was promoted to immutable evidence: %#v", b)
	}
}

func newResumeWakeAuditStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "wake-project", UserID: "wake-owner", Name: "Wake audit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "wake-frame", ProjectID: "wake-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAutoResumeDispatch("wake-frame", "wake-frame", "wake-project", "OPERON", "test_recovery"); err != nil {
		t.Fatal(err)
	}
	return store
}

func parkResumeWakeAudit(t *testing.T, store *Store) CompatibilityFrameResumeDispatch {
	t.Helper()
	claim, claimed, err := store.ClaimNextCompatibilityFrameResumeDispatch("wake-worker", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim=%#v claimed=%t err=%v", claim, claimed, err)
	}
	if _, _, err := store.RequeueCompatibilityFrameResumeDispatch(RequeueCompatibilityFrameResumeDispatchInput{
		ResumeEventID: claim.ResumeEvent.ID, ExpectedAttempt: claim.Attempt, ClaimToken: claim.ClaimToken,
		ReasonCode: "test_recovery", RunnerAttempt: claim.Attempt, CheckpointEventID: int64(claim.Attempt + 10), NotBefore: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	parked, found, err := store.GetCompatibilityFrameResumeDispatch(claim.ResumeEvent.ID)
	if err != nil || !found {
		t.Fatalf("parked=%#v found=%t err=%v", parked, found, err)
	}
	return parked
}

func TestFrameResumeWakeAuditDistinguishesSuccessiveWaits(t *testing.T) {
	store := newResumeWakeAuditStore(t)
	first := parkResumeWakeAudit(t, store)
	a, changed, err := wakeCompatibilityDispatchForTest(store, first.ResumeEvent.ID)
	if err != nil || !changed {
		t.Fatalf("first wake changed=%t err=%v", changed, err)
	}
	second := parkResumeWakeAudit(t, store)
	b, changed, err := wakeCompatibilityDispatchForTest(store, second.ResumeEvent.ID)
	if err != nil || !changed {
		t.Fatalf("second wake changed=%t err=%v", changed, err)
	}
	if a.ID == b.ID || numberValueForWakeTest(b.Payload["dispatchAttempt"]) != second.Attempt {
		t.Fatalf("second wait reused first audit: first=%#v second=%#v want attempt=%d", a, b, second.Attempt)
	}
}

func numberValueForWakeTest(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	}
	return 0
}

func wakeCompatibilityDispatchForTest(store *Store, id string) (FrameEvent, bool, error) {
	dispatch, _, err := store.GetCompatibilityFrameResumeDispatch(id)
	if err != nil {
		return FrameEvent{}, false, err
	}
	ref := dispatch.WaitRef
	if ref == "" {
		ref = "unknown"
	}
	return store.WakeCompatibilityFrameResumeDispatch(FrameResumeDispatchWakeInput{
		ResumeEventID: id, ExpectedWaitRef: ref, Source: FrameResumeWakeLegacyUnknown,
		Cause: FrameResumeWakeCause{Kind: "legacy_unknown", Evidence: "unknown"},
	})
}
