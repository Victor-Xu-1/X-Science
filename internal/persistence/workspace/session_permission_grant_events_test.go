package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSessionPermissionGrantAutomaticSourcesNeverMintWithoutProof(t *testing.T) {
	for _, test := range []struct {
		source, scope string
		admitted      bool
	}{
		{"policy", "conversation", false}, {"remembered", "conversation", false}, {"policy", "once", true},
	} {
		t.Run(test.source+"-"+test.scope, func(t *testing.T) {
			store, _, request := newKernelLocalExecApprovalFixture(t)
			if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			input := kernelLocalExecResolutionForTest(request, true, test.scope)
			input.ApprovalSource = test.source
			_, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), input)
			if test.admitted && (err != nil || !created) || !test.admitted && (!errors.Is(err, ErrApprovalPolicyGrantStale) || created) {
				t.Fatalf("created=%t err=%v", created, err)
			}
			snapshot, err := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
			if err != nil || len(snapshot.Grants) != 0 {
				t.Fatalf("automatic source minted permission=%#v err=%v", snapshot, err)
			}
			assertSessionGrantEvents(t, store, request, 0)
		})
	}
}

func TestSessionPermissionGrantInvalidationsAreCommittedAndReplaySafe(t *testing.T) {
	store, _, request := newKernelLocalExecApprovalFixture(t)
	id := approveSessionGrantForTest(t, store, request, "conversation")
	assertSessionGrantEvents(t, store, request, 1)
	if _, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), kernelLocalExecResolutionForTest(request, true, "conversation")); err != nil || created {
		t.Fatalf("replay created=%t err=%v", created, err)
	}
	assertSessionGrantEvents(t, store, request, 1)
	renew := request
	renew.RequestID, renew.ToolCallID = "renew-event", "renew-event-call"
	id = approveSessionGrantForTest(t, store, renew, "conversation")
	assertSessionGrantEvents(t, store, request, 2)
	input := sessionGrantRevokeForTest(request, []SessionPermissionGrantSelection{{GrantID: id, Kind: "local_exec", Key: request.Tool}})
	for _, wantCount := range []int{3, 3} {
		if _, err := store.RevokeSessionPermissionGrants(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		assertSessionGrantEvents(t, store, request, wantCount)
	}
}

func TestSessionPermissionGrantOutboxFailureRollsBackMutation(t *testing.T) {
	for _, operation := range []string{"mint", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			store, _, request := newKernelLocalExecApprovalFixture(t)
			id := ""
			if operation == "revoke" {
				id = approveSessionGrantForTest(t, store, request, "conversation")
			} else if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`CREATE TRIGGER reject_session_permission_event BEFORE INSERT ON workspace_outbox
				WHEN NEW.event_type='conversation.listChanged' BEGIN SELECT RAISE(ABORT,'test permission event failure'); END`); err != nil {
				t.Fatal(err)
			}
			if operation == "mint" {
				if _, _, _, err := store.ResolveKernelLocalExecApproval(context.Background(), kernelLocalExecResolutionForTest(request, true, "conversation")); err == nil {
					t.Fatal("mint ignored durable invalidation failure")
				}
			} else if _, err := store.RevokeSessionPermissionGrants(context.Background(), sessionGrantRevokeForTest(request, []SessionPermissionGrantSelection{{GrantID: id, Kind: "local_exec", Key: request.Tool}})); err == nil {
				t.Fatal("revoke ignored durable invalidation failure")
			}
			snapshot, err := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "mint" && len(snapshot.Grants) != 0 || operation == "revoke" && (len(snapshot.Grants) != 1 || snapshot.Grants[0].GrantID != id) {
				t.Fatalf("failed outbox mutation committed grant=%#v", snapshot)
			}
			count := 0
			if operation == "revoke" {
				count = 1
			}
			assertSessionGrantEvents(t, store, request, count)
		})
	}
}

func assertSessionGrantEvents(t *testing.T, store *Store, request KernelLocalExecApprovalRequestInput, want int) {
	t.Helper()
	rows, err := store.db.Query(`SELECT payload_json FROM workspace_outbox WHERE event_type='conversation.listChanged' ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		var envelope RealtimeOutboxEnvelope
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Event.Payload["permission_grants_changed"] != true {
			continue
		}
		count++
		if envelope.Event.UserID != request.OwnerUserID || envelope.Event.RootFrameID != request.RootFrameID || envelope.Event.FrameID != request.RootFrameID ||
			envelope.Event.Payload["conversation_id"] != request.RootFrameID || envelope.Event.Payload["root_frame_id"] != request.RootFrameID || envelope.Event.Payload["root_frame_incarnation_id"] != request.RootFrameIncarnationID {
			t.Fatalf("invalidation lost owner/root/incarnation: %#v", envelope)
		}
		if len(envelope.Event.Payload) != 6 {
			t.Fatalf("invalidation exposed non-scope data: %#v", envelope.Event.Payload)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("permission invalidations=%d want=%d", count, want)
	}
}
