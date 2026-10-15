package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestSessionRunnerProviderSubscriptionFailureUsesSafeDurableProjection(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "PRIVATE_REQUEST_SENTINEL")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"InvalidSubscription","type":"","param":"","message":"PRIVATE_ACCOUNT_SENTINEL subscription expired; renew the plan. Never publish this diagnostic."}}`))
	}))
	defer provider.Close()

	root := t.TempDir()
	frameID := "provider-subscription"
	store, srv := newFreshFrameResumeDispatchFixture(t, root, filepath.Join(root, "workspace.db"), provider.URL, frameID, "Inspect retained test output without executing another tool.")
	metadata, _, err := store.GetFrameRuntimeMetadata(frameID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ContextData == nil {
		metadata.ContextData = map[string]any{}
	}
	metadata.ContextData["web_assistant"] = map[string]any{"conversation_overrides": map[string]any{"model": "resume-model"}}
	if _, err := store.SetFrameRuntimeMetadata(frameID, metadata); err != nil {
		t.Fatal(err)
	}
	processing := "processing"
	if _, err := store.UpdateFrame(frameID, workspace.UpdateFrameInput{Status: &processing}); err != nil {
		t.Fatal(err)
	}
	result, err := srv.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: frameID, RunnerID: "subscription-runner", RequireSavedModel: true,
		LeaseTTL: time.Minute, ReplayLimit: 100, OutputLimitBytes: 64 * 1024,
		RequestTimeout: time.Second, MaxAttempts: 3, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	})
	if err != nil || !result.Claimed || result.Status != "interrupted" ||
		result.InterruptionReasonCode != sessionRunnerModelProviderUnavailableReasonCode ||
		result.InterruptionAutoResume || result.FinishEventID != 0 || result.AwaitingModelSelection {
		t.Fatalf("runner=%#v err=%v", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("non-transient subscription requests=%d want=1", requests.Load())
	}
	model, err := srv.sessionConversationModelSnapshot(frameID)
	if err != nil || model.Selection != "resume-model" {
		t.Fatalf("selected model was lost: snapshot=%#v err=%v", model, err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	interruption, paused, err := repo.LatestRunnerInterruption(context.Background(), "frame:"+frameID, "local")
	if err != nil || !paused || interruption.EventID != result.CheckpointEventID || interruption.AutoResume {
		t.Fatalf("durable interruption=%#v paused=%t err=%v", interruption, paused, err)
	}
	events, err := repo.ListProjectedCoordinateEvents(context.Background(), transcriptstore.ListProjectedEventsInput{
		StreamUID: "frame:" + frameID, OwnerID: "local", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpointFound := false
	for _, event := range events {
		if event.Event.EventID != result.CheckpointEventID {
			continue
		}
		checkpointFound = true
		var payload map[string]any
		if err := json.Unmarshal(event.Event.PayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["provider_failure_kind"] != "subscription" {
			t.Errorf("checkpoint did not preserve typed provider category: %#v", payload)
		}
	}
	if !checkpointFound {
		t.Fatal("the exact interruption checkpoint was not readable")
	}
	projected := compatJSONRequest(t, srv.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
	assertProviderFailurePublicProjection(t, projected, "subscription")
	frame, found, err := store.GetCompatibilityFrame(frameID)
	if err != nil || !found {
		t.Fatalf("frame found=%t err=%v", found, err)
	}
	runtime, err := srv.webConversationRuntimeSnapshot(frame)
	if err != nil || runtime["state"] != "paused" || runtime["can_send_message"] != true {
		t.Fatalf("subscription became a missing-model wait: runtime=%#v err=%v", runtime, err)
	}
}

func TestFrameProviderFailureProjectionClassifiesOnlyLegacyHTTPEnvelope(t *testing.T) {
	cases := []struct {
		name, detail, want string
	}{
		{"subscription", `provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"PRIVATE_ACCOUNT_SENTINEL"}}`, "subscription"},
		{"unknown_code", `provider endpoint returned 400: {"error":{"code":"UnknownFailure","message":"subscription expired; PRIVATE_ACCOUNT_SENTINEL"}}`, "unavailable"},
		{"ordinary_text", `The model wrote: provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"PRIVATE_ACCOUNT_SENTINEL"}}`, "unavailable"},
		{"configuration", "No active model configuration is available. Configure or select a model, then continue this same task.", "configuration_missing"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			frameID := "legacy-provider-" + test.name
			store, srv := newFreshFrameResumeDispatchFixture(t, root, filepath.Join(root, "workspace.db"), "http://127.0.0.1:1", frameID, "Keep the existing task and its private failure evidence.")
			processing := "processing"
			if _, err := store.UpdateFrame(frameID, workspace.UpdateFrameInput{Status: &processing}); err != nil {
				t.Fatal(err)
			}
			repo, err := store.TranscriptRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
				StreamUID: "frame:" + frameID, OwnerID: "local", RunnerID: "legacy-provider-runner", TTL: time.Minute,
				ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claim.Claimed {
				t.Fatalf("claim=%#v err=%v", claim, err)
			}
			interrupted, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
				Claim: claim.Claim, ClientMessageID: "legacy-provider-failure",
				ReasonCode: sessionRunnerModelProviderUnavailableReasonCode, ResumeDetail: test.detail,
				AutoResume: false, Resumable: false, Destinations: []string{"ws"},
			})
			if err != nil || !interrupted.Created {
				t.Fatalf("interruption=%#v err=%v", interrupted, err)
			}
			original := append([]byte(nil), interrupted.Event.PayloadJSON...)
			projected := compatJSONRequest(t, srv.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
			assertProviderFailurePublicProjection(t, projected, test.want)
			frame, found, err := store.GetCompatibilityFrame(frameID)
			if err != nil || !found {
				t.Fatalf("frame found=%t err=%v", found, err)
			}
			runtime, err := srv.webConversationRuntimeSnapshot(frame)
			wantState := "paused"
			if test.want == "configuration_missing" {
				wantState = "waiting_input"
			}
			if err != nil || runtime["state"] != wantState || runtime["can_send_message"] != true {
				t.Errorf("public runtime=%#v err=%v want=%s", runtime, err, wantState)
			}
			stored, found, err := repo.GetEventByClientMessageID(context.Background(), "frame:"+frameID, "local", "legacy-provider-failure")
			if err != nil || !found || !bytes.Equal(stored.PayloadJSON, original) {
				t.Fatalf("reading the failure changed private history: found=%t err=%v", found, err)
			}
		})
	}
}

func assertProviderFailurePublicProjection(t *testing.T, projected map[string]any, want string) {
	t.Helper()
	if projected["status"] != "paused" || projected["runtime_paused"] != true || projected["runtime_active"] != false ||
		projected["runtime_interruption_reason"] != sessionRunnerModelProviderUnavailableReasonCode || projected["runtime_provider_failure_kind"] != want {
		t.Errorf("provider pause/category projection=%#v want=%s", projected, want)
	}
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_ACCOUNT_SENTINEL", "PRIVATE_REQUEST_SENTINEL", "InvalidSubscription", "provider endpoint returned 400"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("public frame exposed private provider diagnostic %q", private)
		}
	}
}
