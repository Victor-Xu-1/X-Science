package workspace

import (
	"errors"
	"path/filepath"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func newSessionDelegationPolicyFixture(t *testing.T) (*Store, Frame, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "delegation-policy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "policy-project", UserID: "policy-owner", Name: "Policy fixture"}); err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(CreateFrameInput{ID: "policy-root", ProjectID: "policy-project", AgentName: "OPERON", Status: "completed", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	return store, frame, path
}

func TestSessionDelegationHistoryReplacementAndRestartPreserveDeny(t *testing.T) {
	store, frame, path := newSessionDelegationPolicyFixture(t)
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := delegationPolicyCommand(snapshot, "persistent-deny", "deny")
	first, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	for _, messages := range [][]map[string]any{nil, {{"role": "user", "content": "Retained synthetic message"}}} {
		if err := transcriptstore.NewRepository(store.db).RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
			return replaceCompatibilityRootMessages(t.Context(), tx, frame.ID, messages, store.now().UTC())
		}); err != nil {
			t.Fatal(err)
		}
		current, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
		if err != nil || current.Policy != "deny" || current.Revision != 1 {
			t.Fatalf("history clear/replacement erased policy: %#v %v", current, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retry, err := reopened.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command)
	if err != nil || !retry.Replayed || retry.Receipt != first.Receipt || retry.Snapshot.Policy != "deny" || retry.Snapshot.Revision != 1 {
		t.Fatalf("restart lost immutable deny receipt: %#v %v", retry, err)
	}
}

func TestSessionDelegationReservedEventsCannotBeAppendedOrCopiedToBranch(t *testing.T) {
	store, frame, _ := newSessionDelegationPolicyFixture(t)
	for _, eventType := range []string{sessionDelegationPolicyEventType, sessionDelegationCommandEventType} {
		input := FrameEventInput{FrameID: frame.ID, Type: eventType, Payload: map[string]any{"policy": "allow"}}
		if _, err := store.AppendFrameEvent(input); !errors.Is(err, ErrDelegatedStopInvalid) {
			t.Fatalf("generic event append accepted %q", eventType)
		}
		if _, err := store.AppendFrameOutboxEventTx(t.Context(), nil, input); !errors.Is(err, ErrDelegatedStopInvalid) {
			t.Fatalf("generic transaction append accepted %q", eventType)
		}
	}
	if _, err := store.AppendFrameEvent(FrameEventInput{ID: "delegated:session-policy:forged", FrameID: frame.ID, Type: "user_message"}); !errors.Is(err, ErrDelegatedStopInvalid) {
		t.Fatal("generic append accepted reserved record ID")
	}
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, delegationPolicyCommand(snapshot, "branch-deny", "deny")); err != nil {
		t.Fatal(err)
	}
	branch, err := store.CreateFrameBranch(CreateFrameBranchInput{ID: "policy-branch", RootFrameID: frame.ID, SourceFrameID: frame.ID, Mode: FrameBranchAside, Request: "Synthetic branch"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range branch.Events {
		if event.Type == sessionDelegationPolicyEventType || event.Type == sessionDelegationCommandEventType {
			t.Fatalf("branch copied a root authority record: %#v", event)
		}
	}
	current, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", branch.Frame.ID)
	if err != nil || current.Scope != snapshot.Scope || current.Policy != "deny" || current.Revision != 1 {
		t.Fatalf("branch did not resolve its one root authority: %#v %v", current, err)
	}
}

func TestSessionDelegationOldRootIncarnationCannotControlReplacement(t *testing.T) {
	store, frame, _ := newSessionDelegationPolicyFixture(t)
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := delegationPolicyCommand(snapshot, "old-lifetime-command", "deny")
	if _, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteCompatibilityFrameTree(frame.ID, "policy-owner", frame.IncarnationID); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateFrame(CreateFrameInput{ID: frame.ID, ProjectID: frame.ProjectID, AgentName: "OPERON", Status: "completed", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.IncarnationID == frame.IncarnationID {
		t.Fatal("replacement reused root incarnation")
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", replacement.ID, command); !errors.Is(err, ErrSessionDelegationScopeStale) {
		t.Fatalf("old lifetime command applied to replacement: %v", err)
	}
	current, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", replacement.ID)
	if err != nil || current.Policy != "allow" || current.Revision != 0 {
		t.Fatalf("new independent lifetime inherited old control: %#v %v", current, err)
	}
}

func TestSessionDelegationConcurrentCommandsSerializeAtRevision(t *testing.T) {
	store, frame, _ := newSessionDelegationPolicyFixture(t)
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"concurrent-one", "concurrent-two"} {
		command := delegationPolicyCommand(snapshot, id, "deny")
		go func() {
			<-start
			_, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command)
			results <- err
		}()
	}
	close(start)
	var applied, stale int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			applied++
		case errors.Is(err, ErrSessionDelegationRevision):
			stale++
		default:
			t.Fatalf("concurrent command failed unexpectedly: %v", err)
		}
	}
	if applied != 1 || stale != 1 {
		t.Fatalf("revision accepted competing writes: applied=%d stale=%d", applied, stale)
	}
	var events int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type=?`, frame.ID, sessionDelegationCommandEventType).Scan(&events); err != nil || events != 1 {
		t.Fatalf("conflicting mutation left a receipt: %d %v", events, err)
	}
}

func TestSessionDelegationOutboxFailureRollsBackPolicyAndReceipt(t *testing.T) {
	store, frame, _ := newSessionDelegationPolicyFixture(t)
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := delegationPolicyCommand(snapshot, "atomic-deny", "deny")
	if _, err := store.db.Exec(`CREATE TRIGGER reject_policy_notice BEFORE INSERT ON workspace_outbox
		WHEN NEW.event_type='conversation.listChanged' BEGIN SELECT RAISE(ABORT,'synthetic outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command); err == nil {
		t.Fatal("policy committed without its reliable notification")
	}
	current, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil || current.Policy != "allow" || current.Revision != 0 {
		t.Fatalf("failed notification left partial policy: %#v %v", current, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_policy_notice`); err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command)
	if err != nil || applied.Replayed || applied.Snapshot.Policy != "deny" || applied.Snapshot.Revision != 1 {
		t.Fatalf("failed command left a partial receipt: %#v %v", applied, err)
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), "policy-owner", frame.ID, command); err != nil {
		t.Fatal(err)
	}
	var notifications int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE idempotency_key=?`,
		delegatedRecordID("session-policy-notification", "policy-owner", snapshot.Scope.RootIncarnationID, command.CommandID)).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatalf("retry duplicated reliable notification: count=%d err=%v", notifications, err)
	}
}
