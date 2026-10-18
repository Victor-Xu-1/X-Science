package server

import (
	"net/http"
	"net/url"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestSessionPermissionGrantAPIBootstrapsActualFrameAndRootScope(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "session-grants-project", "session-grants-owner")
	root, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "session-grants-root", ProjectID: "session-grants-project", AgentName: "xscience", Status: "completed", ConversationType: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "session-grants-child", ProjectID: root.ProjectID, ParentFrameID: root.ID, AgentName: "xscience", Status: "completed", ConversationType: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + child.ID + "/permission-grants"
	body := memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "session-grants-owner", http.StatusOK)
	scope, ok := body["scope"].(map[string]any)
	if body["schema"] != "x-science.session-permission-grants.v1" || !ok ||
		scope["frame_id"] != child.ID || scope["frame_incarnation_id"] != child.IncarnationID ||
		scope["root_frame_id"] != root.ID || scope["root_frame_incarnation_id"] != root.IncarnationID || scope["project_id"] != root.ProjectID {
		t.Fatalf("typed scope did not bootstrap actual child and root: %#v", body)
	}
	if grants, ok := body["grants"].([]any); !ok || len(grants) != 0 || body["incomplete"] != false ||
		body["legacy_unbound_count"] != float64(0) || body["stale_grant_count"] != float64(0) {
		t.Fatalf("empty actual session snapshot is not explicit: %#v", body)
	}
	memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "foreign-owner", http.StatusNotFound)
	query := url.Values{
		"expected_frame_incarnation_id": {child.IncarnationID}, "expected_root_frame_id": {root.ID},
		"expected_root_frame_incarnation_id": {"stale-root-incarnation"},
	}
	stale := memoryCompatJSON(t, app.Handler(), http.MethodGet, path+"?"+query.Encode(), nil, "session-grants-owner", http.StatusConflict)
	if stale["code"] != "SESSION_GRANT_SCOPE_STALE" {
		t.Fatalf("wrong stale scope error: %#v", stale)
	}
}

func TestSessionPermissionGrantAPIRejectsPartialScopeAndFreshFilterRevoke(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "session-grants-project", "session-grants-owner")
	frame, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "session-grants-root", ProjectID: "session-grants-project", AgentName: "xscience", Status: "completed", ConversationType: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + frame.ID + "/permission-grants"
	partial := memoryCompatJSON(t, app.Handler(), http.MethodGet, path+"?expected_frame_incarnation_id="+url.QueryEscape(frame.IncarnationID), nil, "session-grants-owner", http.StatusBadRequest)
	if partial["code"] != "SESSION_GRANT_REQUEST_INVALID" {
		t.Fatalf("wrong partial scope error: %#v", partial)
	}
	freshFilter := map[string]any{
		"schema": "x-science.session-permission-revoke.v1", "expected_frame_incarnation_id": frame.IncarnationID,
		"expected_root_frame_id": frame.ID, "expected_root_frame_incarnation_id": frame.IncarnationID,
		"kind": "local_exec", "clear_all": true,
	}
	rejected := memoryCompatJSON(t, app.Handler(), http.MethodPost, path+"/revoke", freshFilter, "session-grants-owner", http.StatusBadRequest)
	if rejected["code"] != "SESSION_GRANT_REQUEST_INVALID" {
		t.Fatalf("fresh filter was not rejected by typed contract: %#v", rejected)
	}
}
