package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestConversationSkillAuthoringDoesNotCorrectNativeRefusal(t *testing.T) {
	const refusalText = "Synthetic provider refusal."
	responsesReply := func(content map[string]any) map[string]any {
		return map[string]any{
			"id": "skill-refusal-fixture", "model": "fixture-author", "status": "completed",
			"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{content}}},
		}
	}
	cases := []struct {
		name         string
		providerType string
		endpoint     string
		body         map[string]any
	}{
		{"chat_refusal", "openai-compatible", "/v1/chat/completions", map[string]any{
			"model": "fixture-author", "choices": []any{map[string]any{
				"finish_reason": "stop", "message": map[string]any{
					"role": "assistant", "content": refusalText, "refusal": refusalText,
				},
			}},
		}},
		{"responses_type", "openai-responses", "/v1/responses", responsesReply(map[string]any{"type": "refusal", "text": refusalText})},
		{"responses_field", "openai-responses", "/v1/responses", responsesReply(map[string]any{"type": "output_text", "refusal": refusalText})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.endpoint {
					http.Error(w, "unexpected refusal fixture endpoint", http.StatusBadRequest)
					return
				}
				if requests.Add(1) > 1 {
					http.Error(w, "native refusal must not be corrected", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(test.body)
			}))
			t.Cleanup(upstream.Close)
			fixture := newConversationSkillAuthoringFixture(t, nil)
			enabled := true
			provider, err := fixture.server.workspaceStore.RegisterModelProvider(workspace.ModelProviderInput{
				ID: "skill-refusal-provider", UserID: "local", Name: "Fixture refusal", Type: test.providerType,
				BaseURL: upstream.URL + "/v1", Model: "fixture-author", Enabled: &enabled,
			})
			if err != nil {
				t.Fatal(err)
			}
			// The shared fixture already installs its default author provider.
			// Select this separate native-protocol fixture rather than inserting
			// the same primary key twice or relying on default-provider order.
			if _, err := fixture.server.workspaceStore.SetFrameRuntimeMetadata(fixture.frameID, workspace.FrameRuntimeMetadata{
				ContextData: map[string]any{"web_assistant": map[string]any{
					"conversation_overrides": map[string]any{"model": "provider:" + provider.ID},
				}},
			}); err != nil {
				t.Fatal(err)
			}
			fixture.server.httpClient = upstream.Client()
			response := fixture.save(context.Background())
			if response.Code != http.StatusBadGateway || requests.Load() != 1 {
				t.Fatalf("native refusal save status=%d requests=%d body=%s", response.Code, requests.Load(), response.Body.String())
			}
			fixture.assertNoInstallation(t)
		})
	}
}
