package workspace

import (
	"context"
	"testing"
)

func TestSessionPermissionGrantRootReplacementDoesNotInheritOldGrant(t *testing.T) {
	for _, fallback := range []string{"none", "project", "always"} {
		t.Run(fallback, func(t *testing.T) {
			store, _, request := newKernelLocalExecApprovalFixture(t)
			grantID := approveSessionGrantForTest(t, store, request, "conversation")
			if fallback != "none" {
				broader := request
				broader.RequestID, broader.ToolCallID = "broader-approval", "broader-call"
				approveSessionGrantForTest(t, store, broader, fallback)
			}
			before, err := store.KernelLocalExecApprovalPolicy(context.Background(), request.OwnerUserID,
				request.ProjectID, request.RootFrameID, request.Tool, request.Environment)
			if err != nil || !before.Found || before.Scope != "conversation" {
				t.Fatalf("real initial conversation grant unavailable: %#v err=%v", before, err)
			}
			oldRoot, found, err := store.GetFrame(request.RootFrameID)
			if err != nil || !found {
				t.Fatalf("old root unavailable: found=%t err=%v", found, err)
			}
			if err := store.DeleteFrameRealtime(context.Background(), oldRoot, request.OwnerUserID, "session-grant-root-deleted"); err != nil {
				t.Fatal(err)
			}
			replacement, err := store.CreateFrame(CreateFrameInput{
				ID: oldRoot.ID, ProjectID: oldRoot.ProjectID, AgentName: oldRoot.AgentName,
				Status: "completed", ConversationType: oldRoot.ConversationType,
			})
			if err != nil {
				t.Fatal(err)
			}
			if replacement.IncarnationID == "" || replacement.IncarnationID == oldRoot.IncarnationID {
				t.Fatal("fixture did not create a real replacement incarnation")
			}
			policy, err := store.KernelLocalExecApprovalPolicy(context.Background(), request.OwnerUserID,
				request.ProjectID, replacement.ID, request.Tool, request.Environment)
			if err != nil {
				t.Fatal(err)
			}
			if fallback == "none" && policy.Found || fallback != "none" && (!policy.Found || policy.Scope != fallback) {
				t.Errorf("old root grant rebound or masked inherited policy: fallback=%s policy=%#v", fallback, policy)
			}
			var retained int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM approval_policy_grants WHERE id=?`, grantID).Scan(&retained); err != nil || retained != 1 {
				t.Errorf("old grant was deleted/migrated instead of remaining unbound: count=%d err=%v", retained, err)
			}
		})
	}
}

func TestSessionPermissionGrantExplicitRenewalChangesGenerationButReplayDoesNot(t *testing.T) {
	store, _, request := newKernelLocalExecApprovalFixture(t)
	first := approveSessionGrantForTest(t, store, request, "conversation")
	resolution := kernelLocalExecResolutionForTest(request, true, "conversation")
	if _, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), resolution); err != nil || created {
		t.Fatalf("same decision replay was not a no-op: created=%t err=%v", created, err)
	}
	if replay := sessionGrantIDForTest(t, store, request, "conversation"); replay != first {
		t.Fatal("same explicit decision replay renewed grant generation")
	}
	renewal := request
	renewal.RequestID, renewal.ToolCallID = "renewal-approval", "renewal-call"
	second := approveSessionGrantForTest(t, store, renewal, "conversation")
	if first == second {
		t.Fatalf("distinct explicit approvals reused one deterministic generation: %s", first)
	}
}

func approveSessionGrantForTest(t *testing.T, store *Store, request KernelLocalExecApprovalRequestInput, scope string) string {
	t.Helper()
	if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), kernelLocalExecResolutionForTest(request, true, scope)); err != nil || !created {
		t.Fatalf("real approval scope=%s created=%t err=%v", scope, created, err)
	}
	return sessionGrantIDForTest(t, store, request, scope)
}

func sessionGrantIDForTest(t *testing.T, store *Store, request KernelLocalExecApprovalRequestInput, scope string) string {
	t.Helper()
	target := ""
	if scope == "conversation" {
		target = request.RootFrameID
	} else if scope == "project" {
		target = request.ProjectID
	}
	var id string
	if err := store.db.QueryRow(`SELECT id FROM approval_policy_grants
		WHERE owner_user_id=? AND kind='local_exec' AND grant_key=? AND scope=? AND scope_target_id=?`,
		request.OwnerUserID, kernelLocalExecGrantKey(request.Tool, request.Environment), scope, target).Scan(&id); err != nil || id == "" {
		t.Fatalf("real grant generation unavailable: id=%q err=%v", id, err)
	}
	return id
}
