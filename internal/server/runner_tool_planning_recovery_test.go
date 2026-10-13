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

	secretstore "x-science/internal/persistence/secrets"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
	"x-science/internal/sessionrunner"
)

func TestToolPlanningPrivateOnlyRecoveryParksWithoutRepeatedGeneration(t *testing.T) {
	for _, savedAllowance := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved_allowance_%t", savedAllowance), func(t *testing.T) {
			testToolPlanningPrivateOnlyRecovery(t, savedAllowance, false, false)
		})
	}
}

func TestToolPlanningPrivateBudgetGrowthCannotExtendRecoveryWindow(t *testing.T) {
	testToolPlanningPrivateOnlyRecovery(t, true, true, false)
}

func TestToolPlanningPartialToolDeadlineKeepsBoundedRecovery(t *testing.T) {
	testToolPlanningPrivateOnlyRecovery(t, false, false, true)
}

func testToolPlanningPrivateOnlyRecovery(t *testing.T, savedAllowance, growingPrivate, partialTool bool) {
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "planning-project", "planning-frame")
	var requests atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence := requests.Add(1)
		var input struct {
			Tools     []json.RawMessage `json:"tools"`
			MaxTokens int               `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if len(input.Tools) == 0 {
			t.Error("fixture did not expose a tool-enabled request")
			return
		}
		if growingPrivate {
			writeProbeLimit(w, providers.ProtocolOpenAICompatible, "private", max(input.MaxTokens, 17))
			return
		}
		if savedAllowance && sequence == 1 {
			if input.MaxTokens != 17 {
				t.Errorf("saved allowance=%d, want 17", input.MaxTokens)
			}
			writeProbeLimit(w, providers.ProtocolOpenAICompatible, "private", 17)
			return
		}
		if input.MaxTokens != 0 {
			t.Errorf("exhausted planning replayed numerical fallback: %d", input.MaxTokens)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if partialTool {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"partial-call\",\"type\":\"function\",\"function\":{\"name\":\"ask_user\",\"arguments\":\"{\"}}]}}]}\n\n")
			w.(http.Flusher).Flush()
		}
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
	var allowance *int
	if savedAllowance {
		value := 17
		allowance = &value
	}
	if _, err := store.RegisterModelProvider(workspace.ModelProviderInput{
		ID: "planning-provider", UserID: "local", Name: "Planning", Type: "openai",
		BaseURL: api.URL + "/v1", Model: "planning-model", SecretRef: "secret://planning-key", Enabled: &enabled, MaxTokens: allowance,
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
	wantRequests := int64(1)
	if savedAllowance {
		wantRequests = 2
	}
	wantReason := "provider_stream_no_progress"
	if growingPrivate {
		wantRequests = sessionrunner.MaxRecoveryStreak
		wantReason = sessionRunnerProviderOutputTokenLimitReasonCode
	}
	if partialTool {
		wantRequests = sessionrunner.MaxRecoveryStreak
	}
	for cycle := int64(1); cycle <= wantRequests; cycle++ {
		options.RunnerID = fmt.Sprintf("planning-runner-%d", cycle)
		result, err := srv.RunSessionRunnerChatOnce(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status == "completed" {
			t.Fatal("private reasoning was published as a completed answer")
		}
		if result.AwaitingRecoveryCondition && !result.InterruptionAutoResume {
			if cycle != wantRequests || result.InterruptionReasonCode != wantReason {
				t.Fatalf("wrong recovery boundary: cycle=%d result=%+v", cycle, result)
			}
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
			if requests.Load() != before || before != wantRequests {
				t.Fatalf("parked task repeated provider calls: before=%d after=%d", before, requests.Load())
			}
			if partialTool {
				events, err := repo.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, Limit: 1000})
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range events {
					var payload map[string]any
					if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
						t.Fatal(err)
					}
					if payload["toolPhase"] == "start" || payload["toolPhase"] == "completed" || payload["toolPhase"] == "waiting" {
						t.Fatalf("incomplete tool arguments reached execution or approval: phase=%v", payload["toolPhase"])
					}
				}
			}
			if growingPrivate || partialTool {
				return
			}
			checkpoint, found, err := repo.LatestResumableCheckpoint(ctx, stream.UID, stream.OwnerID)
			if err != nil || !found {
				t.Fatalf("resumable checkpoint lost: %t %v", found, err)
			}
			// A deliberate checkpoint resume remains possible. It gets its own
			// action window, but no retired numerical budget or automatic replay.
			options.SessionID = "planning-frame"
			options.RunnerID = "planning-manual-resume"
			options.TranscriptResumeSource = transcriptstore.ResumeSourceCheckpoint
			options.TranscriptCheckpoint = checkpoint.Sequence
			manual, err := srv.RunSessionRunnerChatOnce(ctx, options)
			if err != nil || requests.Load() != before+1 || !manual.AwaitingRecoveryCondition || manual.InterruptionAutoResume {
				t.Fatalf("manual recovery was frozen or lost park boundary: %+v requests=%d err=%v", manual, requests.Load(), err)
			}
			if _, _, err := srv.submitFrameMessage(store, frameMessageSubmission{
				FrameID: "planning-frame", MessageUUID: "planning-new-message", ClientMessageID: "planning-new-input", Text: "Use the updated input for the next action.",
			}); err != nil {
				t.Fatal(err)
			}
			options.TranscriptResumeSource = ""
			options.TranscriptCheckpoint = 0
			options.RunnerID = "planning-new-input"
			if _, err := srv.RunSessionRunnerChatOnce(ctx, options); err != nil || requests.Load() != before+2 {
				t.Fatalf("new input was permanently frozen: requests=%d err=%v", requests.Load(), err)
			}
			return
		}
		if !result.InterruptionAutoResume {
			t.Fatalf("saved length probe was not resumable: %+v", result)
		}
		options.SessionID = ""
	}
	t.Fatal("private-only generation did not reach a resumable parked state")
}
