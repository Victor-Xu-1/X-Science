package server

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestSessionPermissionGrantLegacyDeleteRequiresObservedGeneration(t *testing.T) {
	app, store, request := newSessionPermissionApprovalFixture(t)
	resolveSessionPermissionApprovalForTest(t, store, request, "conversation")
	before, err := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
	if err != nil || len(before.Grants) != 1 {
		t.Fatalf("snapshot=%#v err=%v", before, err)
	}
	result := memoryCompatJSON(t, app.Handler(), http.MethodDelete, "/api/approvals/grants", map[string]any{
		"kind": "local_exec", "key": request.Tool, "scope": "conversation", "tier": "allow", "rootFrameId": request.RootFrameID,
	}, request.OwnerUserID, http.StatusConflict)
	if result["code"] != "SESSION_GRANT_VERSIONED_REVOKE_REQUIRED" || result["replacement"] != "/api/conversations/{frame}/permission-grants/revoke" {
		t.Fatalf("missing explicit replacement=%#v", result)
	}
	after, err := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy delete changed session generation: %#v err=%v", after, err)
	}
}

func TestSessionPermissionGrantLegacyMixedBulkHasZeroSideEffects(t *testing.T) {
	app, store, request := newSessionPermissionApprovalFixture(t)
	// The project python entry sorts before the conversation r entry. A
	// per-item guard after deletion would already have damaged broader policy.
	resolveSessionPermissionApprovalForTest(t, store, request, "project")
	conversation := request
	conversation.RequestID, conversation.ToolCallID, conversation.Tool = "conversation-r", "conversation-r-call", "r"
	if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), conversation); err != nil {
		t.Fatal(err)
	}
	resolveSessionPermissionApprovalForTest(t, store, conversation, "conversation")
	before, err := app.loadCompatibilityPermissionGrants(request.OwnerUserID)
	if err != nil || len(before) != 2 || before[0].Scope != "project" {
		t.Fatalf("fixture order=%#v err=%v", before, err)
	}
	result := memoryCompatJSON(t, app.Handler(), http.MethodDelete, "/api/approvals/grants/all", map[string]any{"kind": "local_exec"}, request.OwnerUserID, http.StatusConflict)
	if result["code"] != "SESSION_GRANT_VERSIONED_REVOKE_REQUIRED" {
		t.Fatalf("wrong bulk refusal=%#v", result)
	}
	after, err := app.loadCompatibilityPermissionGrants(request.OwnerUserID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("mixed bulk partially removed policy: before=%#v after=%#v err=%v", before, after, err)
	}
}

func resolveSessionPermissionApprovalForTest(t *testing.T, store *workspace.Store, request workspace.KernelLocalExecApprovalRequestInput, scope string) {
	t.Helper()
	_, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), workspace.KernelLocalExecApprovalResolutionInput{
		ApprovalSource: "user",
		OwnerUserID:    request.OwnerUserID, ProjectID: request.ProjectID, FrameID: request.FrameID, FrameIncarnationID: request.FrameIncarnationID,
		RootFrameID: request.RootFrameID, RootIncarnationID: request.RootFrameIncarnationID, RequestID: request.RequestID, Tool: request.Tool,
		Environment: request.Environment, InputSHA256: request.InputSHA256, StreamUID: request.StreamUID, RunnerID: request.RunnerID,
		RunnerAttempt: request.RunnerAttempt, ClaimTokenSHA256: request.ClaimTokenSHA256, KernelID: request.KernelID, ExpectedGeneration: request.ExpectedGeneration,
		Approved: true, Scope: scope,
	})
	if err != nil || !created {
		t.Fatalf("real approval created=%t err=%v", created, err)
	}
}
