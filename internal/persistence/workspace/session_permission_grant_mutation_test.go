package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestSessionPermissionGrantFixedSetPreservesRenewalAndBroaderPolicy(t *testing.T) {
	store, _, request := newKernelLocalExecApprovalFixture(t)
	first := approveSessionGrantForTest(t, store, request, "conversation")
	secondRequest := request
	secondRequest.RequestID, secondRequest.ToolCallID, secondRequest.Tool = "second", "second-call", "r"
	second := approveSessionGrantForTest(t, store, secondRequest, "conversation")
	broader := request
	broader.RequestID, broader.ToolCallID = "project-allow", "project-call"
	projectID := approveSessionGrantForTest(t, store, broader, "project")
	renewed := request
	renewed.RequestID, renewed.ToolCallID = "renewed", "renewed-call"
	renewalID := approveSessionGrantForTest(t, store, renewed, "conversation")
	later := request
	later.RequestID, later.ToolCallID, later.Tool = "later", "later-call", "repl"
	laterID := approveSessionGrantForTest(t, store, later, "conversation")
	input := sessionGrantRevokeForTest(request, []SessionPermissionGrantSelection{
		{GrantID: first, Kind: "local_exec", Key: request.Tool}, {GrantID: second, Kind: "local_exec", Key: secondRequest.Tool},
	})
	wake := store.KernelRetentionWake()
	result, err := store.RevokeSessionPermissionGrants(context.Background(), input)
	if err != nil || len(result.Results) != 2 || result.Results[0].Status != "changed" || result.Results[1].Status != "revoked" {
		t.Fatalf("fixed set mutation=%#v err=%v", result, err)
	}
	if len(result.Snapshot.Grants) != 2 || !result.BroaderPoliciesUnchanged {
		t.Fatalf("incorrect readback=%#v", result)
	}
	if current := sessionGrantIDForTest(t, store, request, "conversation"); current != renewalID {
		t.Fatal("old snapshot removed renewal")
	}
	if current := sessionGrantIDForTest(t, store, later, "conversation"); current != laterID {
		t.Fatal("Clear all removed later unseen grant")
	}
	if current := sessionGrantIDForTest(t, store, broader, "project"); current != projectID {
		t.Fatal("session revoke touched project policy")
	}
	select {
	case <-wake:
	default:
		t.Fatal("committed revoke did not wake existing recovery")
	}
	replayWake := store.KernelRetentionWake()
	replay, err := store.RevokeSessionPermissionGrants(context.Background(), input)
	if err != nil || replay.Results[0].Status != "changed" || replay.Results[1].Status != "already_absent" {
		t.Fatalf("lost-ACK replay=%#v err=%v", replay, err)
	}
	select {
	case <-replayWake:
		t.Fatal("no-op replay generated a spurious recovery wake")
	default:
	}
}

func TestSessionPermissionGrantExactScopeRejectsForeignAndReplacement(t *testing.T) {
	store, _, request := newKernelLocalExecApprovalFixture(t)
	id := approveSessionGrantForTest(t, store, request, "conversation")
	input := sessionGrantRevokeForTest(request, []SessionPermissionGrantSelection{{GrantID: id, Kind: "local_exec", Key: request.Tool}})
	foreign := input
	foreign.OwnerID = "foreign-owner"
	if _, err := store.RevokeSessionPermissionGrants(context.Background(), foreign); !errors.Is(err, ErrSessionGrantTargetUnavailable) {
		t.Fatalf("foreign owner err=%v", err)
	}
	stale := input
	stale.Expected.RootFrameIncarnationID = "old-root"
	if _, err := store.RevokeSessionPermissionGrants(context.Background(), stale); !errors.Is(err, ErrSessionGrantScopeStale) {
		t.Fatalf("stale root err=%v", err)
	}
	if current := sessionGrantIDForTest(t, store, request, "conversation"); current != id {
		t.Fatal("rejected scope changed grant")
	}
}

func TestSessionPermissionGrantLegacyUnboundRowsRemainStoredAndFallThrough(t *testing.T) {
	store, _, request := newKernelLocalExecApprovalFixture(t)
	projectID := approveSessionGrantForTest(t, store, request, "project")
	// A historical row with no incarnation proof is an import fixture, not a
	// current approval; it must never be silently upgraded during a read.
	_, err := store.db.Exec(`INSERT INTO approval_policy_grants(id,owner_user_id,kind,grant_key,scope,tier,scope_target_id,created_at_ms,updated_at_ms)
		VALUES('local-exec:legacy-import',?,'local_exec',?,'conversation','deny',?,1,1)`, request.OwnerUserID, request.Tool, request.RootFrameID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.KernelLocalExecApprovalPolicy(context.Background(), request.OwnerUserID, request.ProjectID, request.RootFrameID, request.Tool, request.Environment)
	if err != nil || !policy.Found || policy.Scope != "project" || policy.GrantProof == nil || policy.GrantProof.ID != projectID {
		t.Fatalf("legacy masked inherited policy=%#v err=%v", policy, err)
	}
	snapshot, err := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
	if err != nil || len(snapshot.Grants) != 0 || snapshot.LegacyUnboundCount != 1 {
		t.Fatalf("legacy falsely became session permission=%#v err=%v", snapshot, err)
	}
	var id string
	if err := store.db.QueryRow(`SELECT id FROM approval_policy_grants WHERE scope='conversation'`).Scan(&id); err != nil || id != "local-exec:legacy-import" {
		t.Fatalf("legacy mutated: id=%s err=%v", id, err)
	}
}

func sessionGrantRevokeForTest(request KernelLocalExecApprovalRequestInput, grants []SessionPermissionGrantSelection) SessionPermissionGrantRevokeInput {
	return SessionPermissionGrantRevokeInput{OwnerID: request.OwnerUserID, FrameID: request.FrameID,
		Expected: SessionPermissionExpectedScope{FrameIncarnationID: request.FrameIncarnationID, RootFrameID: request.RootFrameID, RootFrameIncarnationID: request.RootFrameIncarnationID}, Grants: grants}
}
