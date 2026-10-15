package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestSessionRunnerPermanentAccount429DoesNotRetryOrAutoResume(t *testing.T) {
	for _, test := range []struct{ code, kind string }{
		{"InvalidSubscription", "subscription"},
		{"invalid_api_key", "authentication"},
		{"access_denied", "access_denied"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			var requests atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": test.code, "message": "PRIVATE_ACCOUNT_SENTINEL repair account status"}})
			}))
			defer provider.Close()
			frameID := "permanent-account-" + test.kind
			_, srv, repo, _ := newProviderFollowupFrame(t, provider.URL, frameID)
			result, err := srv.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
				SessionID: frameID, RunnerID: "permanent-account-runner", RequireSavedModel: true,
				LeaseTTL: time.Minute, ReplayLimit: 100, OutputLimitBytes: 64 * 1024,
				RequestTimeout: time.Second, MaxAttempts: 2, MaxToolRounds: 1, DisableSkillDiscovery: true, DisableMCPDiscovery: true,
			})
			if err != nil || !result.Claimed || result.Status != "interrupted" || result.FinishEventID != 0 || result.AwaitingModelSelection {
				t.Fatalf("runner=%#v err=%v", result, err)
			}
			if requests.Load() != 1 || result.InterruptionReasonCode != sessionRunnerModelProviderUnavailableReasonCode || result.InterruptionAutoResume {
				t.Errorf("permanent account rejection followed capacity retries: requests=%d reason=%s auto=%t", requests.Load(), result.InterruptionReasonCode, result.InterruptionAutoResume)
			}
			selected, err := srv.sessionConversationModelSnapshot(frameID)
			if err != nil || selected.Selection != "resume-model" {
				t.Fatalf("selection changed: %#v err=%v", selected, err)
			}
			interruption, found, err := repo.LatestRunnerInterruption(context.Background(), "frame:"+frameID, "local")
			if err != nil || !found || interruption.EventID != result.CheckpointEventID || interruption.AutoResume || interruption.ProviderFailureKind != test.kind {
				t.Errorf("durable account condition=%#v found=%t err=%v", interruption, found, err)
			}
			if _, found, err := repo.GetAutoResumeCandidate(context.Background(), "frame:"+frameID, "local"); err != nil || found {
				t.Errorf("permanent account condition remains automatic: found=%t err=%v", found, err)
			}
			projected := compatJSONRequest(t, srv.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
			assertProviderFailurePublicProjection(t, projected, test.kind)
		})
	}
}

func TestFrameOptionalProviderCategoryDoesNotBlockExplicitContinue(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer provider.Close()
	frameID := "optional-provider-continue"
	store, srv, repo, databasePath := newProviderFollowupFrame(t, provider.URL, frameID)
	claim, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: "frame:" + frameID, OwnerID: "local", RunnerID: "optional-provider-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	interruption, err := repo.InterruptRunner(context.Background(), transcriptstore.InterruptRunnerInput{
		Claim: claim.Claim, ClientMessageID: "optional-provider-interruption", ReasonCode: sessionRunnerModelProviderUnavailableReasonCode,
		ResumeDetail: `provider endpoint returned 400: {"error":{"code":"InvalidSubscription","message":"PRIVATE_ACCOUNT_SENTINEL"}}`,
		AutoResume:   false, Resumable: true,
	})
	if err != nil || !interruption.Created {
		t.Fatalf("interruption=%#v err=%v", interruption, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(interruption.Event.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	payload["provider_failure_kind"] = "future_provider_category"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	// Private historical-annotation fixture; the real user DB is never opened.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE transcript_events SET payload_json=? WHERE stream_uid=? AND event_id=?`, raw, claim.Claim.StreamUID, interruption.Event.EventID); err != nil {
		t.Fatal(err)
	}
	projected := compatJSONRequest(t, srv.Handler(), http.MethodGet, "/api/frames/"+frameID, "local", nil, http.StatusOK)
	assertProviderFailurePublicProjection(t, projected, "subscription")
	// This helper fetches and submits the real authenticated, target-bound
	// Continue command; it does not forge a target or bypass the dispatcher.
	compatJSONRequest(t, srv.Handler(), http.MethodPost, "/api/frames/"+frameID+"/resume", "local", map[string]any{}, http.StatusOK)
	dispatch, found, err := store.GetCompatibilityFrameResumeDispatchByFrame(frameID)
	if err != nil || !found || dispatch.Status != "registered" || requests.Load() != 0 {
		t.Fatalf("continue dispatched incorrectly: status=%s found=%t requests=%d err=%v", dispatch.Status, found, requests.Load(), err)
	}
	var stored []byte
	if err := db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, claim.Claim.StreamUID, interruption.Event.EventID).Scan(&stored); err != nil || !bytes.Equal(stored, raw) {
		t.Fatalf("Continue changed the historical diagnostic: err=%v", err)
	}
}

func newProviderFollowupFrame(t *testing.T, providerURL, frameID string) (*workspace.Store, *Server, *transcriptstore.Repository, string) {
	t.Helper()
	root := t.TempDir()
	databasePath := filepath.Join(root, "workspace.db")
	store, srv := newFreshFrameResumeDispatchFixture(t, root, databasePath, providerURL, frameID, "Preserve this fixture task and its completed work.")
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
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store, srv, repo, databasePath
}
