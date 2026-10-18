package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManualNotebookPublicRequestFailsClosedBeforeRegistration(t *testing.T) {
	store, manager, app, access := newManualNotebookHTTPRuntime(t)
	base := manualPublicRequestBody(t, manager, access)
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown field", func(v map[string]any) { v["background"] = true }},
		{"missing schema", func(v map[string]any) { delete(v, "schema") }},
		{"missing generation", func(v map[string]any) { delete(v, "expected_kernel_generation") }},
		{"missing incarnation", func(v map[string]any) { delete(v, "expected_frame_incarnation_id") }},
		{"zero timeout", func(v map[string]any) { v["timeout_ms"] = 0 }},
		{"invalid UUID", func(v map[string]any) { v["request_id"] = "not-a-request-uuid" }},
		{"oversized source", func(v map[string]any) { v["code"] = strings.Repeat("a", 100001) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := manualPublicCopyBody(base)
			test.mutate(body)
			kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusBadRequest)
		})
	}
	t.Run("body limit", func(t *testing.T) {
		body := manualPublicCopyBody(base)
		body["code"] = strings.Repeat("x", 1<<20)
		kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusRequestEntityTooLarge)
	})
	t.Run("trailing JSON", func(t *testing.T) {
		raw, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		request := newLoopbackTestRequest(http.MethodPost, manualPublicCollection, bytes.NewReader(append(raw, []byte(" {}")...)))
		request.Header.Set("X-Science-User-Id", access.UserID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("trailing JSON status=%d body=%s", response.Code, response.Body.String())
		}
	})
	kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, "", base, http.StatusUnauthorized)
	kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, "different-owner", base, http.StatusNotFound)
	for _, key := range []string{"expected_frame_incarnation_id", "expected_root_frame_incarnation_id", "expected_root_frame_id"} {
		body := manualPublicCopyBody(base)
		body[key] = "stale-identity"
		kernelAPIRequest(t, app, http.MethodPost, manualPublicCollection, access.UserID, body, http.StatusConflict)
	}
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("rejected public request registered a worker execution")
	}
	events, err := store.ListFrameEvents(access.Frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "kernel_manual_execution" {
			t.Fatalf("invalid request created durable admission: %#v", event)
		}
	}
}
