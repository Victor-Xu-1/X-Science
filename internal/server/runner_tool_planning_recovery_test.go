package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	secretstore "synon-go/internal/persistence/secrets"
	workspace "synon-go/internal/persistence/workspace"
)

func TestToolPlanningPrivateOnlyRecoveryParksWithoutRepeatedGeneration(t *testing.T) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "planning-project", "planning-frame")
	var requests atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var input struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if len(input.Tools) == 0 {
			t.Error("fixture did not expose a tool-enabled request")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer api.Close()
	srv := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	if _, err := srv.settingsStore.Set("model.activeProviderId", "planning-provider"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.secretStore.Create(secretstore.Secret{ID: "planning-key", UserID: "local", Provider: "openai", Value: "local-test-only"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := store.RegisterModelProvider(workspace.ModelProviderInput{
		ID: "planning-provider", UserID: "local", Name: "Planning", Type: "openai",
		BaseURL: api.URL + "/v1", Model: "planning-model", SecretRef: "secret://planning-key", Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "planning-frame", MessageUUID: "planning-message", ClientMessageID: "planning-input",
		Text: "Perform the next analysis and preserve existing work.",
	}); err != nil {
		t.Fatal(err)
	}
	options := SessionRunnerChatOptions{SessionID: "planning-frame", LeaseTTL: time.Minute, RequestTimeout: 100 * time.Millisecond,
		MaxAttempts: 3, RequireSavedModel: true, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
		AllowedTools: []string{"todo_write", "ask_user"}}
	ctx := context.Background()
	for cycle := 1; cycle <= 8; cycle++ {
		options.RunnerID = fmt.Sprintf("planning-runner-%d", cycle)
		result, err := srv.RunSessionRunnerChatOnce(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "completed" {
			t.Fatal("private reasoning was published as a completed answer")
		}
		if result.AwaitingRecoveryCondition && !result.InterruptionAutoResume {
			stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "planning-frame")
			if err != nil || !found {
				t.Fatalf("recovery stream found=%t err=%v", found, err)
			}
			if _, eligible, err := repo.GetAutoResumeCandidate(ctx, stream.UID, stream.OwnerID); err != nil || eligible {
				t.Fatalf("unchanged private-only generation stayed dispatchable: %t %v", eligible, err)
			}
			before := requests.Load()
			options.SessionID = ""
			if _, err := srv.RunSessionRunnerChatOnce(ctx, options); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != before || before > 8 {
				t.Fatalf("parked task repeated provider calls: before=%d after=%d", before, requests.Load())
			}
			return
		}
		options.SessionID = ""
	}
	t.Fatal("private-only generation did not reach a resumable parked state")
}
