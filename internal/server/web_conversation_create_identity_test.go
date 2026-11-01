package server

import (
	"net/http"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestWebConversationCreateHandsOffCurrentSessionIdentity(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	project := createP3Project(t, store, "create-handoff-project", "local")
	response := p3JSONRequest(t, app, http.MethodPost, "/api/conversations", map[string]any{
		"name": "New session controls",
		"extra": map[string]any{
			"project_id": project.ID, "frame_incarnation_id": "untrusted-incarnation",
			"root_frame_id": "untrusted-root", "pinned": true, "selected_view": "timeline",
		},
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	created := p3DecodeObject(t, response)
	frameID := webString(created["id"])
	frame, found, err := store.GetCompatibilityFrame(frameID)
	if err != nil || !found || frame.IncarnationID == "" {
		t.Fatalf("missing durable identity: found=%t err=%v", found, err)
	}
	extra, _ := created["extra"].(map[string]any)
	if extra["frame_incarnation_id"] != frame.IncarnationID || extra["root_frame_id"] != frame.RootFrameID ||
		extra["root_frame_id"] != frame.ID || extra["presentation_revision"] != float64(0) ||
		extra["pinned"] != true || extra["selected_view"] != "timeline" {
		t.Fatalf("create is not an authoritative navigation handoff: %#v", extra)
	}
	// The create response itself must be enough to authorize the existing
	// session-control protocol; there is no compensating conversation-detail GET.
	path := "/api/frames/" + frameID + "/delegation-policy"
	policy := memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "local", http.StatusOK)
	scope, _ := policy["scope"].(map[string]any)
	if scope["root_frame_id"] != extra["root_frame_id"] || scope["root_incarnation_id"] != extra["frame_incarnation_id"] {
		t.Fatalf("create identity disagrees with session controls: create=%#v policy=%#v", extra, policy)
	}
	command := map[string]any{
		"schema": workspace.SessionDelegationCommandSchema, "command_id": "create-handoff-deny",
		"scope":             map[string]any{"root_frame_id": extra["root_frame_id"], "root_incarnation_id": extra["frame_incarnation_id"]},
		"expected_revision": policy["revision"], "desired_policy": "deny",
	}
	changed := memoryCompatJSON(t, app.Handler(), http.MethodPost, path, command, "local", http.StatusOK)
	if snapshot, _ := changed["snapshot"].(map[string]any); snapshot["policy"] != "deny" {
		t.Fatalf("create handoff did not reach current policy authority: %#v", changed)
	}
	memoryCompatJSON(t, app.Handler(), http.MethodGet, path, nil, "other-owner", http.StatusNotFound)
	if detail := p3JSONRequest(t, app, http.MethodGet, "/api/conversations/"+frameID, nil, ""); detail.Code != http.StatusOK ||
		p3DecodeObject(t, detail)["extra"].(map[string]any)["frame_incarnation_id"] != extra["frame_incarnation_id"] {
		t.Fatalf("create/detail identity diverged: %d %s", detail.Code, detail.Body.String())
	}
}
