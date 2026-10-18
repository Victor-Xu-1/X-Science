package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestConversationPresentationMetadataRejectsRecreatedFrameAfterAccessRead(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	input := workspace.CreateFrameInput{ID: "metadata-aba", ProjectID: "presentation-project", AgentName: "OPERON", Status: "completed", ConversationType: "agent", Name: "Original"}
	if _, err := store.CreateFrame(input); err != nil {
		t.Fatal(err)
	}
	captured, found, err := store.GetCompatibilityFrame(input.ID)
	if err != nil || !found {
		t.Fatalf("capture access projection: found=%t err=%v", found, err)
	}
	if err := store.DeleteFrame(input.ID); err != nil {
		t.Fatal(err)
	}
	input.Name = "Replacement"
	replacement, err := store.CreateFrame(input)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.IncarnationID == captured.IncarnationID {
		t.Fatal("fixture did not recreate the frame identity")
	}
	// Resume the handler with the real access projection captured before deletion.
	for _, body := range []string{`{"name":"Late rename"}`, `{"desc":"Late description"}`} {
		request := newLoopbackTestRequest(http.MethodPatch, "/api/conversations/"+input.ID, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Science-User-Id", "local")
		response := httptest.NewRecorder()
		app.handleWebConversationPresentationUpdate(response, request, captured)
		if response.Code != http.StatusConflict || p3DecodeObject(t, response)["code"] != "CONVERSATION_PRESENTATION_STALE" {
			t.Fatalf("late metadata overwrite: %d %s", response.Code, response.Body.String())
		}
	}
	current, _, err := store.GetCompatibilityFrame(input.ID)
	if err != nil || current.Name != replacement.Name || current.TaskSummary != "" || !current.UpdatedAt.Equal(replacement.UpdatedAt) {
		t.Fatalf("replacement changed: %#v err=%v", current, err)
	}
}

func TestConversationPresentationBatchRequiresRevisionButAcceptsExplicitLegacyZero(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	createP3Project(t, store, "presentation-project", "local")
	frame := presentationTestFrame(t, store, "legacy-pinned-order")
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, workspace.FrameRuntimeMetadata{
		ContextData: map[string]any{"web_extra": map[string]any{"pinned": true}},
	}); err != nil {
		t.Fatal(err)
	}
	item := map[string]any{"id": frame.ID, "expected_incarnation_id": frame.IncarnationID, "sortOrder": 1000}
	body := map[string]any{"pinned_order": []any{item}}
	path := "/api/conversations/" + frame.ID
	for _, test := range []struct {
		name         string
		explicitNull bool
	}{{"missing", false}, {"null", true}} {
		if test.explicitNull {
			item["expected_presentation_revision"] = nil
		}
		response := p3JSONRequest(t, app, http.MethodPatch, path, body, "")
		if response.Code != http.StatusBadRequest || p3DecodeObject(t, response)["code"] != "CONVERSATION_PRESENTATION_INVALID" {
			t.Fatalf("%s revision accepted: %d %s", test.name, response.Code, response.Body.String())
		}
	}
	metadata, _, err := store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := workspace.ConversationPresentation(metadata.ContextData)
	if err != nil || view["presentation_revision"] != int64(0) || view["sortOrder"] != nil {
		t.Fatalf("missing revision mutated legacy state: %#v err=%v", view, err)
	}
	item["expected_presentation_revision"] = 0
	response := p3JSONRequest(t, app, http.MethodPatch, path, body, "")
	if response.Code != http.StatusOK {
		t.Fatalf("explicit zero rejected: %d %s", response.Code, response.Body.String())
	}
	metadata, _, err = store.GetFrameRuntimeMetadata(frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err = workspace.ConversationPresentation(metadata.ContextData)
	if err != nil || view["presentation_revision"] != int64(1) || view["sortOrder"] != float64(1000) {
		t.Fatalf("legacy zero CAS not applied: %#v err=%v", view, err)
	}
}
