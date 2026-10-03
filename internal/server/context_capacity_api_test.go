package server

import (
	"net/http"
	"testing"
)

func TestContextCapacityProfileAPIExplicitUnknownAndIdentityIsolation(t *testing.T) {
	app, store := newP3WebConversationServer(t)
	input := map[string]any{"id": "capacity-api", "name": "Capacity API", "provider": "custom", "baseUrl": "https://provider.invalid/v1", "model": "routed-alias", "contextWindow": 128000, "maxTokens": 2048}
	response := p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "")
	profile, _ := p3DecodeObject(t, response)["profile"].(map[string]any)
	if response.Code != http.StatusOK || profile["contextWindow"] != float64(128000) || profile["maxTokens"] != float64(2048) {
		t.Fatalf("context profile HTTP=%d %+v", response.Code, profile)
	}
	delete(input, "contextWindow")
	delete(input, "maxTokens")
	response = p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "")
	profile, _ = p3DecodeObject(t, response)["profile"].(map[string]any)
	if profile["contextWindow"] != float64(128000) {
		t.Fatal("omitted capacity did not preserve same model")
	}
	input["model"] = "other-model"
	response = p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "")
	profile, _ = p3DecodeObject(t, response)["profile"].(map[string]any)
	if profile["contextWindow"] != nil || profile["maxTokens"] != float64(2048) {
		t.Fatalf("identity reused window or changed output: %+v", profile)
	}
	for _, invalid := range []any{0, 10000001, 1.5, "128000"} {
		input["contextWindow"] = invalid
		response = p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid capacity %v HTTP=%d", invalid, response.Code)
		}
	}
	input["contextWindow"] = 64000
	foreign := p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "other-owner")
	if foreign.Code == http.StatusOK {
		t.Fatal("foreign capacity update accepted")
	}
	provider, found, err := store.GetModelProvider("local", "capacity-api")
	if err != nil || !found || provider.ContextWindow != nil {
		t.Fatalf("invalid/foreign update changed capacity: %+v %v", provider, err)
	}
	input["contextWindow"] = nil
	response = p3JSONRequest(t, app, http.MethodPost, "/api/llm/providers", input, "")
	if response.Code != http.StatusOK {
		t.Fatalf("explicit unknown reset HTTP=%d", response.Code)
	}
}
