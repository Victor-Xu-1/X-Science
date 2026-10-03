package workspace

import (
	"path/filepath"
	"testing"
)

func TestModelProviderContextWindowPersistsAndInvalidatesOnIdentityChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	window, output := 128000, 2048
	input := ModelProviderInput{ID: "context-provider", UserID: "owner", Name: "primary", Type: "custom", BaseURL: "https://model.invalid/v1", Model: "routed-alias", ContextWindow: &window, MaxTokens: &output}
	provider, err := store.UpsertModelProvider(input)
	if err != nil || provider.ContextWindow == nil || *provider.ContextWindow != window {
		t.Fatalf("declared capacity: %+v %v", provider, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider, found, err := store.GetModelProvider("owner", input.ID)
	if err != nil || !found || provider.ContextWindow == nil || *provider.ContextWindow != window {
		t.Fatalf("reopened capacity: %+v %v", provider, err)
	}
	input.ContextWindow = nil
	provider, err = store.UpsertModelProvider(input)
	if err != nil || provider.ContextWindow == nil {
		t.Fatalf("omitted capacity lost: %+v %v", provider, err)
	}
	input.UserID = "other"
	if _, err := store.UpsertModelProvider(input); err == nil {
		t.Fatal("cross-owner capacity update accepted")
	}
	input.UserID = "owner"
	for _, field := range []string{"model", "endpoint", "type"} {
		input.ContextWindow, input.ContextWindowSet = &window, true
		if _, err := store.UpsertModelProvider(input); err != nil {
			t.Fatal(err)
		}
		input.ContextWindow, input.ContextWindowSet = nil, false
		switch field {
		case "model":
			input.Model = "another-model"
		case "endpoint":
			input.BaseURL = "https://other.invalid/v1"
		case "type":
			input.Type = "openai-compatible"
		}
		provider, err = store.UpsertModelProvider(input)
		if err != nil || provider.ContextWindow != nil {
			t.Fatalf("%s reused old capacity: %+v %v", field, provider, err)
		}
		if provider.MaxTokens == nil || *provider.MaxTokens != output {
			t.Fatal("capacity invalidation changed output limit")
		}
	}
	input.ContextWindow, input.ContextWindowSet = &window, true
	if _, err := store.UpsertModelProvider(input); err != nil {
		t.Fatal(err)
	}
	input.ContextWindow = nil
	provider, err = store.UpsertModelProvider(input)
	if err != nil || provider.ContextWindow != nil {
		t.Fatalf("explicit unknown reset: %+v %v", provider, err)
	}
	for _, invalid := range []int{0, -1, 10000001} {
		input.ContextWindow = &invalid
		if _, err := store.UpsertModelProvider(input); err == nil {
			t.Fatalf("invalid capacity accepted: %d", invalid)
		}
	}
}
