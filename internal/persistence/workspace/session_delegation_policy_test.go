package workspace

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func delegationPolicyCommand(snapshot SessionDelegationSnapshot, id, policy string) SessionDelegationCommand {
	return SessionDelegationCommand{Schema: SessionDelegationCommandSchema, CommandID: id,
		Scope: snapshot.Scope, ExpectedRevision: snapshot.Revision, DesiredPolicy: policy}
}

func TestSessionDelegationLegacyGuidanceIsNotPolicy(t *testing.T) {
	store, _, _, parent := delegatedStartedInvocation(t)
	for _, value := range []any{nil, false, true} {
		if _, err := store.SetFrameRuntimeMetadata(parent.FrameID, FrameRuntimeMetadata{
			ContextData: map[string]any{"_original_input": map[string]any{"ultra_mode": value}},
		}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID)
		if err != nil || snapshot.Policy != "allow" || snapshot.Revision != 0 || snapshot.HasLiveDelegatedAttempts {
			t.Fatalf("legacy guidance changed authority: %#v %v", snapshot, err)
		}
	}
}

func TestSessionDelegationCommandRetrySeparatesReceiptAndCurrentReadback(t *testing.T) {
	store, _, _, parent := delegatedStartedInvocation(t)
	initial, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	deny := delegationPolicyCommand(initial, "deny-command", "deny")
	first, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID, deny)
	if err != nil || first.Replayed || first.Snapshot.Policy != "deny" || first.Snapshot.Revision != 1 {
		t.Fatalf("first command=%#v %v", first, err)
	}
	second, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID,
		delegationPolicyCommand(first.Snapshot, "allow-command", "allow"))
	if err != nil || second.Snapshot.Policy != "allow" || second.Snapshot.Revision != 2 {
		t.Fatalf("second command=%#v %v", second, err)
	}
	retry, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID, deny)
	if err != nil || !retry.Replayed || retry.Receipt != first.Receipt || retry.Snapshot.Policy != "allow" || retry.Snapshot.Revision != 2 {
		t.Fatalf("old retry replaced current policy: %#v %v", retry, err)
	}
	changed := deny
	changed.DesiredPolicy = "allow"
	if _, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID, changed); !errors.Is(err, ErrSessionDelegationConflict) {
		t.Fatalf("command ID accepted different payload: %v", err)
	}
	stale := delegationPolicyCommand(first.Snapshot, "stale-command", "deny")
	_, err = store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID, stale)
	var conflict *SessionDelegationRevisionError
	if !errors.As(err, &conflict) || conflict.Snapshot.Revision != 2 || conflict.Snapshot.Policy != "allow" {
		t.Fatalf("stale revision did not return current authority: %#v %v", conflict, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE frame_id=? AND event_type=?`, parent.FrameID, sessionDelegationCommandEventType).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retry/conflict wrote another receipt: %d %v", count, err)
	}
}

func TestSessionDelegationDenyPreservesAcceptedChildrenReplay(t *testing.T) {
	store, _, claim, parent := delegatedStartedInvocation(t)
	requests := []KernelDelegateRequest{{Task: "Synthetic accepted child"}}
	admission, err := store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "accepted-call", "outer-call", claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	input := CreateKernelDelegatesInput{Admission: admission, ParentFrameID: parent.FrameID,
		OwnerUserID: parent.OwnerID, ToolUseID: "accepted-call", Requests: requests}
	children, err := store.CreateKernelSupervisedChildren(t.Context(), input)
	if err != nil || len(children) != 1 {
		t.Fatalf("accept original children: %#v %v", children, err)
	}
	initial, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID)
	if err != nil || !initial.HasLiveDelegatedAttempts {
		t.Fatalf("queued accepted child is not live: %#v %v", initial, err)
	}
	denied, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID,
		delegationPolicyCommand(initial, "disable-new-children", "deny"))
	if err != nil || !denied.Snapshot.HasLiveDelegatedAttempts {
		t.Fatalf("disable lost live child: %#v %v", denied, err)
	}
	replayed, err := store.CreateKernelSupervisedChildren(t.Context(), input)
	if err != nil || len(replayed) != 1 || replayed[0].FrameID != children[0].FrameID || replayed[0].Status != "processing" {
		t.Fatalf("new deny rejected an accepted replay: %#v %v", replayed, err)
	}
	input.Admission, err = store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "new-call", "outer-call", claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	input.ToolUseID = "new-call"
	if _, err := store.CreateKernelSupervisedChildren(t.Context(), input); !errors.Is(err, ErrSessionDelegationDisabled) {
		t.Fatalf("deny allowed new children: %v", err)
	}
	input.Admission, input.ToolUseID = nil, "direct-call"
	if _, err := store.CreateKernelSupervisedChildren(t.Context(), input); !errors.Is(err, ErrSessionDelegationDisabled) {
		t.Fatalf("unbound admission bypassed deny: %v", err)
	}
	active, err := store.ListKernelActiveChildren(t.Context(), parent.FrameID, parent.OwnerID)
	if err != nil || len(active) != 1 || active[0].FrameID != children[0].FrameID || active[0].Dispatched {
		t.Fatalf("deny cancelled/duplicated accepted queued children: %#v %v", active, err)
	}
}

func TestSessionDelegationCapturedButUnacceptedCallObservesNewDeny(t *testing.T) {
	store, _, claim, parent := delegatedStartedInvocation(t)
	requests := []KernelDelegateRequest{{Task: "Synthetic not-yet-accepted child"}}
	admission, err := store.CaptureKernelDelegationAdmission(t.Context(), parent.FrameID, parent.OwnerID, "pending-call", "outer-call", claim, requests)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID,
		delegationPolicyCommand(snapshot, "deny-before-insert", "deny")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		Admission: admission, ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID,
		ToolUseID: "pending-call", Requests: requests,
	}); !errors.Is(err, ErrSessionDelegationDisabled) {
		t.Fatalf("capture was incorrectly treated as accepted children: %v", err)
	}
}

func TestSessionDelegationCorruptOrMissingEstablishedPolicyFailsClosed(t *testing.T) {
	for _, damage := range []string{"corrupt", "missing-head", "missing-policy-field"} {
		t.Run(damage, func(t *testing.T) {
			store, _, _, parent := delegatedStartedInvocation(t)
			snapshot, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID,
				delegationPolicyCommand(snapshot, "establish-deny", "deny")); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE frame_events SET payload='{}' WHERE frame_id=? AND event_type=?`
			if damage == "missing-head" {
				query = `DELETE FROM frame_events WHERE frame_id=? AND event_type=?`
			} else if damage == "missing-policy-field" {
				query = `UPDATE frame_events SET payload=json_remove(payload,'$.policy') WHERE frame_id=? AND event_type=?`
			}
			if _, err := store.db.Exec(query, parent.FrameID, sessionDelegationPolicyEventType); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, parent.FrameID); !errors.Is(err, ErrSessionDelegationUnavailable) {
				t.Fatalf("damaged deny became implicit allow: %v", err)
			}
		})
	}
}

func TestSessionDelegationLiveDescendantsRemainManageableAfterDeny(t *testing.T) {
	store, _, _, parent := delegatedStartedInvocation(t)
	children, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		ParentFrameID: parent.FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "live-child",
		Requests: []KernelDelegateRequest{{Task: "Synthetic parent child"}},
	})
	if err != nil || len(children) != 1 {
		t.Fatalf("create child: %#v %v", children, err)
	}
	descendants, err := store.CreateKernelSupervisedChildren(t.Context(), CreateKernelDelegatesInput{
		ParentFrameID: children[0].FrameID, OwnerUserID: parent.OwnerID, ToolUseID: "live-descendant",
		Requests: []KernelDelegateRequest{{Task: "Synthetic descendant"}}, MaxDepth: 2,
	})
	if err != nil || len(descendants) != 1 {
		t.Fatalf("create descendant: %#v %v", descendants, err)
	}
	// Persist the supervisor's ordinary waiting state while its direct parent is
	// already terminal. The readback must inspect the whole root, not one frame.
	if _, err := store.db.Exec(`UPDATE kernel_child_supervision SET status=CASE WHEN frame_id=? THEN 'completed' ELSE 'awaiting_user_response' END WHERE root_frame_id=?`, children[0].FrameID, parent.RootFrameID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SessionDelegationPolicy(t.Context(), parent.OwnerID, children[0].FrameID)
	if err != nil || !snapshot.HasLiveDelegatedAttempts {
		t.Fatalf("waiting descendant was hidden: %#v %v", snapshot, err)
	}
	if _, err := store.ApplySessionDelegationCommand(t.Context(), parent.OwnerID, parent.FrameID, delegationPolicyCommand(snapshot, "deny-new-descendants", "deny")); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkKernelChildDispatched(t.Context(), descendants[0].FrameID, parent.OwnerID); err != nil {
		t.Fatalf("deny prevented dispatch of accepted child: %v", err)
	}
	active, err := store.ListKernelActiveChildren(t.Context(), children[0].FrameID, parent.OwnerID)
	if err != nil || len(active) != 1 || active[0].Status != "awaiting_user_response" || !active[0].Dispatched {
		t.Fatalf("deny cancelled or hid existing descendants: %#v %v", active, err)
	}
}

type delegationCancelEstablishedRead struct {
	*sql.Tx
	cancel context.CancelFunc
	reads  int
}

func (tx *delegationCancelEstablishedRead) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	tx.reads++
	if tx.reads == 2 {
		tx.cancel()
	}
	return tx.Tx.QueryRowContext(ctx, query, args...)
}

func TestSessionDelegationUnavailablePreservesCancellationCause(t *testing.T) {
	store, frame, _ := newSessionDelegationPolicyFixture(t)
	snapshot, err := store.SessionDelegationPolicy(t.Context(), "policy-owner", frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := delegationPolicyCommand(snapshot, "cancelled-command", "deny")
	readers := map[string]func(context.Context, workspaceTransaction) error{
		"scope": func(ctx context.Context, tx workspaceTransaction) error {
			_, _, err := sessionDelegationScopeTx(ctx, tx, "policy-owner", frame.ID)
			return err
		},
		"policy": func(ctx context.Context, tx workspaceTransaction) error {
			_, _, err := sessionDelegationPolicyRecordTx(ctx, tx, "policy-owner", snapshot.Scope)
			return err
		},
		"command": func(ctx context.Context, tx workspaceTransaction) error {
			_, _, err := sessionDelegationReceiptTx(ctx, tx, "policy-owner", command)
			return err
		},
		"live-attempts": func(ctx context.Context, tx workspaceTransaction) error {
			_, err := sessionDelegationSnapshotTx(ctx, tx, sessionDelegationPolicyRecord{Version: 1, OwnerID: "policy-owner", Scope: snapshot.Scope, Policy: "allow"})
			return err
		},
	}
	for name, read := range readers {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(name+"/"+cause.Error(), func(t *testing.T) {
				tx, err := store.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				ctx, cancel := context.WithCancel(t.Context())
				if cause == context.DeadlineExceeded {
					cancel()
					ctx, cancel = context.WithDeadline(t.Context(), time.Unix(1, 0))
				} else {
					cancel()
				}
				defer cancel()
				if err := read(ctx, tx); !errors.Is(err, ErrSessionDelegationUnavailable) || !errors.Is(err, cause) {
					t.Fatalf("policy error lost cancellation/deadline cause: %v", err)
				}
			})
		}
	}
	t.Run("absent-policy-established-query", func(t *testing.T) {
		tx, err := store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, _, err = sessionDelegationPolicyRecordTx(ctx, &delegationCancelEstablishedRead{Tx: tx, cancel: cancel}, "policy-owner", snapshot.Scope)
		if !errors.Is(err, ErrSessionDelegationUnavailable) || !errors.Is(err, context.Canceled) {
			t.Fatalf("policy absence probe lost cancellation cause: %v", err)
		}
	})
}
