package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func resourceTagHTTPReceipt(t *testing.T, response *httptest.ResponseRecorder, action string) workspace.ResourceTagReceipt {
	t.Helper()
	var value workspace.ResourceTagReceipt
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Schema != "x-science.resource-tag-receipt.v1" || value.Action != action || value.OperationID == "" || value.Snapshot.Schema != "x-science.resource-tags.v1" {
		t.Fatalf("tag %s HTTP%d: %s", action, response.Code, response.Body.String())
	}
	return value
}

func resourceTagHTTPFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	app, store := newP3WebConversationServer(t)
	const owner = "resource-tag-http-owner"
	item, err := store.CreateManualLiterature(t.Context(), owner, workspace.LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Tag HTTP resource", Metadata: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return app, owner, item.Item.ID
}

func TestResourceTagsHTTPPersistsAssignmentsAndReplaysExactReceipt(t *testing.T) {
	app, owner, itemID := resourceTagHTTPFixture(t)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		return p3JSONRequest(t, app, method, path, body, owner)
	}
	initial := request(http.MethodGet, "/api/tags", nil)
	var snapshot workspace.ResourceTagSnapshot
	if initial.Code != 200 || json.Unmarshal(initial.Body.Bytes(), &snapshot) != nil || len(snapshot.Tags) != 1 || snapshot.Tags[0].SystemKey != "favorite" || snapshot.Revision != 0 || len(snapshot.Assignments) != 0 {
		t.Fatalf("initial snapshot HTTP%d: %s", initial.Code, initial.Body.String())
	}
	create := workspace.ResourceTagCreate{OperationID: uuid.NewString(), Name: "  Ｒｅｓｅａｒｃｈ\u00a0Notes ", IconKey: "flask-conical", ColorKey: "purple"}
	first := request(http.MethodPost, "/api/tags", create)
	created := resourceTagHTTPReceipt(t, first, "create")
	if created.TagID == "" || len(created.Snapshot.Tags) != 2 || created.Snapshot.Tags[1].Name != "Research Notes" {
		t.Fatalf("create: %+v", created)
	}
	assign := workspace.ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: created.TagID, ResourceType: "literature.item", ResourceID: itemID, Assigned: true}
	assignedResponse := request(http.MethodPut, "/api/tags/assignments", assign)
	assigned := resourceTagHTTPReceipt(t, assignedResponse, "assignment")
	if len(assigned.Snapshot.Assignments) != 1 || assigned.Snapshot.Assignments[0].ResourceID != itemID {
		t.Fatal("canonical item assignment missing")
	}
	update := workspace.ResourceTagUpdate{OperationID: uuid.NewString(), ExpectedUpdatedAt: created.Snapshot.Tags[1].UpdatedAt, Name: "Reviewed", IconKey: "bookmark", ColorKey: "green"}
	updated := resourceTagHTTPReceipt(t, request(http.MethodPatch, "/api/tags/"+created.TagID, update), "update")
	if updated.Snapshot.Tags[1].UpdatedAt <= update.ExpectedUpdatedAt {
		t.Fatal("CAS timestamp was not advanced")
	}
	if stale := request(http.MethodPatch, "/api/tags/"+created.TagID, workspace.ResourceTagUpdate{OperationID: uuid.NewString(), ExpectedUpdatedAt: update.ExpectedUpdatedAt, Name: "Lost edit", IconKey: "tag", ColorKey: "blue"}); stale.Code != 409 {
		t.Fatalf("stale edit accepted: %d", stale.Code)
	}
	for _, replay := range []struct {
		method, path string
		body         any
		expected     *httptest.ResponseRecorder
	}{
		{http.MethodPost, "/api/tags", create, first},
		{http.MethodPut, "/api/tags/assignments", assign, assignedResponse},
	} {
		got := request(replay.method, replay.path, replay.body)
		if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), replay.expected.Body.Bytes()) {
			t.Fatal("lost response retry did not replay the exact original receipt")
		}
	}
	create.Name = "Reused operation with different content"
	if got := request(http.MethodPost, "/api/tags", create); got.Code != 409 {
		t.Fatalf("operation identity changed: %d", got.Code)
	}
	foreign := p3JSONRequest(t, app, http.MethodGet, "/api/tags", nil, "independent-account")
	if foreign.Code != 200 || json.Unmarshal(foreign.Body.Bytes(), &snapshot) != nil || len(snapshot.Tags) != 1 || len(snapshot.Assignments) != 0 {
		t.Fatalf("account data leaked: %d %s", foreign.Code, foreign.Body.String())
	}
	foreignWrite := p3JSONRequest(t, app, http.MethodPut, "/api/tags/assignments", workspace.ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: "tag-favorite", ResourceType: "literature.item", ResourceID: itemID, Assigned: true}, "independent-account")
	if foreignWrite.Code != 404 {
		t.Fatalf("foreign resource assigned: %d", foreignWrite.Code)
	}
	for _, resourceID := range []string{itemID, "nonexistent-resource"} {
		unassigned := p3JSONRequest(t, app, http.MethodPut, "/api/tags/assignments", workspace.ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: "tag-favorite", ResourceType: "literature.item", ResourceID: resourceID, Assigned: false}, "independent-account")
		value := resourceTagHTTPReceipt(t, unassigned, "assignment")
		if len(value.Snapshot.Assignments) != 0 {
			t.Fatal("unassign exposed or changed a foreign relationship")
		}
	}
	removed := resourceTagHTTPReceipt(t, request(http.MethodDelete, "/api/tags/"+created.TagID, map[string]string{"operation_id": uuid.NewString()}), "delete")
	if len(removed.Snapshot.Assignments) != 0 || len(removed.Snapshot.Tags) != 1 {
		t.Fatal("delete did not cascade the exact tag's assignments")
	}
	if got := request(http.MethodPost, "/api/tags", workspace.ResourceTagCreate{OperationID: create.OperationID, Name: "  Ｒｅｓｅａｒｃｈ\u00a0Notes ", IconKey: "flask-conical", ColorKey: "purple"}); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("replay after deletion resurrected a tag or lost its receipt")
	}
	last := request(http.MethodGet, "/api/tags", nil)
	if json.Unmarshal(last.Body.Bytes(), &snapshot) != nil || len(snapshot.Tags) != 1 {
		t.Fatal("old receipt replay resurrected the deleted tag")
	}
}

func TestResourceTagsHTTPRejectsInvalidEnvelopeAndProtectedWrites(t *testing.T) {
	app, owner, itemID := resourceTagHTTPFixture(t)
	unauthenticated := httptest.NewRecorder()
	app.handleResourceTags(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/tags", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous tag access returned %d", unauthenticated.Code)
	}
	for _, test := range []struct {
		name, method, path string
		body               any
		status             int
	}{
		{"query", http.MethodGet, "/api/tags?owner_user_id=other", nil, 400},
		{"unknown actor", http.MethodPost, "/api/tags", map[string]any{"operation_id": uuid.NewString(), "name": "A", "icon_key": "tag", "color_key": "blue", "owner_user_id": "other"}, 400},
		{"invalid operation", http.MethodPost, "/api/tags", map[string]string{"operation_id": "not-a-uuid", "name": "A", "icon_key": "tag", "color_key": "blue"}, 400},
		{"implicit desired value", http.MethodPut, "/api/tags/assignments", map[string]string{"operation_id": uuid.NewString(), "tag_id": "tag-favorite", "resource_type": "literature.item", "resource_id": itemID}, 400},
		{"missing order", http.MethodPut, "/api/tags/order", map[string]string{"operation_id": uuid.NewString()}, 400},
		{"unsupported resource", http.MethodPut, "/api/tags/assignments", workspace.ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: "tag-favorite", ResourceType: "catalog.skill", ResourceID: itemID, Assigned: true}, 400},
		{"favorite delete", http.MethodDelete, "/api/tags/tag-favorite", map[string]string{"operation_id": uuid.NewString()}, 409},
		{"favorite edit", http.MethodPatch, "/api/tags/tag-favorite", workspace.ResourceTagUpdate{OperationID: uuid.NewString(), ExpectedUpdatedAt: 1, Name: "Fake", IconKey: "tag", ColorKey: "blue"}, 409},
		{"directory method", http.MethodPut, "/api/tags", nil, 405},
		{"nested path", http.MethodPatch, "/api/tags/anything/unbound", map[string]string{"operation_id": uuid.NewString()}, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := p3JSONRequest(t, app, test.method, test.path, test.body, owner)
			if got.Code != test.status {
				t.Fatalf("got HTTP%d want%d: %s", got.Code, test.status, got.Body.String())
			}
		})
	}
	for _, raw := range []string{
		`{"operation_id":"` + uuid.NewString() + `","name":"A","icon_key":"tag","color_key":"blue"} {}`,
		`{"operation_id":"` + uuid.NewString() + `","name":"` + strings.Repeat("x", 64<<10) + `","icon_key":"tag","color_key":"blue"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(raw))
		request.Header.Set("X-Science-User-Id", owner)
		response := httptest.NewRecorder()
		app.handleResourceTags(response, request)
		if response.Code != 400 {
			t.Fatalf("noncanonical envelope returned %d", response.Code)
		}
	}
}
