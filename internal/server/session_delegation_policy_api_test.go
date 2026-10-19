package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func newSessionDelegationAPIFixture(t *testing.T) (*Server, *workspace.Store, workspace.Frame) {
	t.Helper()
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "policy-api-project", "policy-api-owner")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{ID: "policy-api-root", ProjectID: "policy-api-project", AgentName: "xscience", Status: "completed", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	return app, store, frame
}

func TestSessionDelegationAPIAuthorityRetryAndCurrentSnapshot(t *testing.T) {
	app, store, root := newSessionDelegationAPIFixture(t)
	child, err := store.CreateFrame(workspace.CreateFrameInput{ID: "policy-api-child", ProjectID: root.ProjectID, ParentFrameID: root.ID, AgentName: "xscience", Status: "completed", ConversationType: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/frames/" + child.ID + "/delegation-policy"
	initial := memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "policy-api-owner", http.StatusOK)
	scope, ok := initial["scope"].(map[string]any)
	if !ok || scope["root_frame_id"] != root.ID || scope["root_incarnation_id"] != root.IncarnationID ||
		initial["schema"] != workspace.SessionDelegationPolicySchema || initial["policy"] != "allow" || initial["revision"] != float64(0) || initial["has_live_delegated_attempts"] != false {
		t.Fatalf("GET did not bootstrap current empty-session authority: %#v", initial)
	}
	deny := map[string]any{"schema": workspace.SessionDelegationCommandSchema, "command_id": "api-deny", "scope": scope, "expected_revision": 0, "desired_policy": "deny"}
	first := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, deny, "policy-api-owner", http.StatusOK)
	if first["schema"] != workspace.SessionDelegationMutationSchema || first["replayed"] != false {
		t.Fatalf("missing mutation receipt: %#v", first)
	}
	allow := map[string]any{"schema": workspace.SessionDelegationCommandSchema, "command_id": "api-allow", "scope": scope, "expected_revision": 1, "desired_policy": "allow"}
	memoryCompatJSON(t, app.Handler(), http.MethodPost, path, allow, "policy-api-owner", http.StatusOK)
	retry := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, deny, "policy-api-owner", http.StatusOK)
	current := retry["snapshot"].(map[string]any)
	receipt := retry["receipt"].(map[string]any)
	if retry["replayed"] != true || current["policy"] != "allow" || current["revision"] != float64(2) || receipt["applied_policy"] != "deny" || receipt["applied_revision"] != float64(1) {
		t.Fatalf("old command replay replaced current readback: %#v", retry)
	}
	stale := map[string]any{"schema": workspace.SessionDelegationCommandSchema, "command_id": "api-stale", "scope": scope, "expected_revision": 0, "desired_policy": "deny"}
	conflict := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, stale, "policy-api-owner", http.StatusConflict)
	if conflict["code"] != "SESSION_DELEGATION_REVISION_CONFLICT" || conflict["snapshot"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("revision conflict did not return current authority: %#v", conflict)
	}
	deny["desired_policy"] = "allow"
	if conflict := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, deny, "policy-api-owner", http.StatusConflict); conflict["code"] != "SESSION_DELEGATION_COMMAND_CONFLICT" {
		t.Fatalf("command ID was mutable: %#v", conflict)
	}
	stale["scope"] = map[string]any{"root_frame_id": root.ID, "root_incarnation_id": "old-root-incarnation"}
	if conflict := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, stale, "policy-api-owner", http.StatusConflict); conflict["code"] != "SESSION_DELEGATION_SCOPE_STALE" {
		t.Fatalf("old incarnation was accepted: %#v", conflict)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rejected := memoryCompatJSON(t, app.Handler(), method, path, allow, "foreign-owner", http.StatusNotFound); rejected["code"] != "SESSION_DELEGATION_TARGET_UNAVAILABLE" {
			t.Fatalf("foreign owner received policy authority: %#v", rejected)
		}
	}
	response := memoryCompatRequest(t, app.Handler(), http.MethodGet, path, nil, "policy-api-owner", http.StatusOK)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("policy readback may be cached")
	}
}

func TestSessionDelegationAPIStrictCommandContract(t *testing.T) {
	app, _, root := newSessionDelegationAPIFixture(t)
	path := "/api/frames/" + root.ID + "/delegation-policy"
	command := workspace.SessionDelegationCommand{Schema: workspace.SessionDelegationCommandSchema, CommandID: "strict-command",
		Scope: workspace.SessionDelegationScope{RootFrameID: root.ID, RootIncarnationID: root.IncarnationID}, ExpectedRevision: 0, DesiredPolicy: "deny"}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(raw)
	for name, body := range map[string]string{
		"unknown-owner":          strings.TrimSuffix(valid, "}") + `,"owner_id":"foreign-owner"}`,
		"trailing-value":         valid + `{}`,
		"fractional-revision":    strings.Replace(valid, `"expected_revision":0`, `"expected_revision":0.5`, 1),
		"missing-scope":          strings.Replace(valid, root.IncarnationID, "", 1),
		"guidance-is-not-policy": `{"ultra_mode":false}`,
		"null":                   `null`,
	} {
		t.Run(name, func(t *testing.T) {
			request := newLoopbackTestRequest(http.MethodPost, path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Science-User-Id", "policy-api-owner")
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"SESSION_DELEGATION_INVALID"`) {
				t.Fatalf("invalid command accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
	memoryCompatJSON(t, app.Handler(), http.MethodGet, path+"?policy=deny", nil, "policy-api-owner", http.StatusBadRequest)
	memoryCompatJSON(t, app.Handler(), http.MethodPatch, path, command, "policy-api-owner", http.StatusMethodNotAllowed)
	current := memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "policy-api-owner", http.StatusOK)
	if current["policy"] != "allow" || current["revision"] != float64(0) {
		t.Fatalf("rejected requests changed policy: %#v", current)
	}
}

func TestSessionDelegationDisabledHostCallIsPermissionDenied(t *testing.T) {
	classified := classifyKernelHostError(fmt.Errorf("create delegated children: %w", workspace.ErrSessionDelegationDisabled))
	var hostError *kernelruntime.HostCallError
	if !errors.As(classified, &hostError) || hostError.Code != "permission_denied" {
		t.Fatalf("explicit deny was misreported as storage failure: %#v", classified)
	}
}
