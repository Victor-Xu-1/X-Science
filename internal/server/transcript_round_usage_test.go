package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	runtimekv "synon-go/internal/persistence/runtimekv"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/providers"
)

func TestRoundUsageHistoryLiveAndLaterRounds(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.runtimeStore = runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	record := func(protocol, model string, input, read, write, output, total int) {
		now := time.Now().UTC()
		f.server.recordSessionRunnerModelAudit(f.stream.SessionID, int(f.claim.Attempt), providers.AuditRecord{
			Protocol: protocol, Model: model, HTTPStatus: 200, StartedAt: now, FinishedAt: now,
			PromptTokens: input, CacheReadTokens: read, CacheWriteTokens: write, CompletionTokens: output, TotalTokens: total,
		})
	}
	record(providers.ProtocolOpenAICompatible, "model-a", 100, 80, 0, 10, 110)
	record(providers.ProtocolAnthropic, "model-b", 12, 30, 5, 3, 50)
	message := roundPresentationMessage(f)
	if err := f.server.enrichTranscriptWebConversationMessages(context.Background(), f.stream.FrameID, "", []map[string]any{message}); err != nil {
		t.Fatal(err)
	}
	if message["round_summary"] != nil {
		t.Fatal("running round claimed a completed summary")
	}
	projection := finishRoundPresentation(t, f, "completed")
	if err := f.server.enrichTranscriptWebConversationMessages(context.Background(), f.stream.FrameID, "", []map[string]any{message}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(message["round_summary"])
	var summary map[string]any
	if err := json.Unmarshal(raw, &summary); err != nil || summary == nil {
		t.Fatalf("completed answer has no round summary: %s %v", raw, err)
	}
	tokens, _ := summary["tokens"].(map[string]any)
	if summary["call_count"] != float64(2) || summary["usage_state"] != "complete" || tokens["input"] != float64(32) || tokens["cache_read"] != float64(110) || tokens["cache_write"] != float64(5) || tokens["output"] != float64(13) || tokens["total"] != float64(160) {
		t.Fatalf("round usage: %s", raw)
	}
	if summary["completed_at"] == nil || summary["elapsed_ms"] == nil {
		t.Fatal("durable timing missing")
	}
	fc, found, err := f.store.GetFrameRealtimeContext(f.stream.FrameID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := f.server.publishTranscriptTerminal(context.Background(), fc, "round-usage-live", projection, webString(message["id"])); err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type == "message.stream" && event.Payload["terminal_status"] == "completed" {
			live, _ := json.Marshal(event.Payload["round_summary"])
			var normalized map[string]any
			_ = json.Unmarshal(live, &normalized)
			if !reflect.DeepEqual(normalized, summary) {
				t.Fatalf("live/history mismatch: %s / %s", live, raw)
			}
			matched = true
		}
	}
	if !matched {
		t.Fatal("no live completion publication")
	}
	page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
	if page.Code != http.StatusOK {
		t.Fatalf("history API: %d %s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `"round_summary"`) {
		t.Fatalf("API lost round summary: %s", page.Body.String())
	}
	foreign := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, "foreign-owner")
	if foreign.Code == http.StatusOK {
		t.Fatal("foreign owner read round summary")
	}
	// A late audit and a later round cannot alter a delivered round.
	record(providers.ProtocolOpenAICompatible, "late-model", 999, 0, 0, 9, 1008)
	startNextPresentationRound(t, f)
	record(providers.ProtocolOpenAICompatible, "next-model", 500, 0, 0, 10, 510)
	if err := f.server.enrichTranscriptWebConversationMessages(context.Background(), f.stream.FrameID, "", []map[string]any{message}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(message["round_summary"])
	if !reflect.DeepEqual(raw, after) {
		t.Fatalf("old summary changed: %s / %s", raw, after)
	}
}

func TestRoundUsageProviderProtocolAndUnavailableCalls(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	f.server.runtimeStore = runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"real-protocol","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"complete"}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":80}}}`))
	}))
	defer upstream.Close()
	client, err := providers.NewRuntimeModelClient(providers.ModelProfile{Provider: providers.ProviderProfile{ID: "test", Type: "openai-compatible", Protocol: providers.ProtocolOpenAICompatible, Endpoint: upstream.URL}, Model: "actual-protocol-model", Request: providers.RequestProfile{MaxAttempts: 1}}, upstream.Client(), func(record providers.AuditRecord) {
		f.server.recordSessionRunnerModelAudit(f.stream.SessionID, int(f.claim.Attempt), record)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Complete(context.Background(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "read-only test"}}}); err != nil {
		t.Fatal(err)
	}
	f.server.recordSessionRunnerModelAudit(f.stream.SessionID, int(f.claim.Attempt), providers.AuditRecord{Protocol: providers.ProtocolOpenAICompatible, Model: "unreported-model", HTTPStatus: 502})
	finishRoundPresentation(t, f, "completed")
	summaries, err := f.server.completedRoundSummaries(context.Background(), f.stream.UID, f.stream.OwnerID, f.stream.SessionID, "", []int64{f.claim.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	got := summaries[f.claim.Attempt]
	if got.CallCount != 2 || got.ReportedCallCount != 1 || got.UsageState != "partial" || got.Tokens.Input != 20 || got.Tokens.Total != 110 {
		t.Fatalf("provider-to-round=%+v", got)
	}
	if _, err := f.server.completedRoundSummaries(context.Background(), f.stream.UID, "wrong", f.stream.SessionID, "", []int64{f.claim.Attempt}); err == nil {
		t.Fatal("owner check bypassed")
	}
}

func TestRoundUsageIncludesResumedAttemptsNotFutureInputs(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	first := f.claim.Attempt
	finishRoundPresentation(t, f, "failed")
	status := "failed"
	if _, err := f.store.UpdateFrame(f.stream.FrameID, workspace.UpdateFrameInput{Status: &status}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ResumeCompatibilityFrameConversation(f.stream.RootFrameID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, claimed, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("round-usage-resume", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("resume dispatch: %t %v", claimed, err)
	}
	input := transcriptstore.ClaimRunnerInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput}
	claim, err := f.repo.ClaimRunner(context.Background(), input)
	if err != nil || !claim.Claimed {
		t.Fatalf("resume: %+v %v", claim, err)
	}
	f.claim = claim.Claim
	finishRoundPresentation(t, f, "completed")
	rounds, err := f.repo.CompletedRoundUsageAuthorities(context.Background(), f.stream.UID, f.stream.OwnerID, "", []int64{first, f.claim.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 || !reflect.DeepEqual(rounds[f.claim.Attempt].Attempts, []int64{first, f.claim.Attempt}) {
		t.Fatalf("rounds=%+v", rounds)
	}
}

func TestRoundUsageMissingCorruptAndProtocolSemantics(t *testing.T) {
	for _, tc := range []struct {
		protocol                                string
		input, read, write, output, total, want int
	}{
		{providers.ProtocolAnthropic, 20, 80, 5, 10, 115, 20},
		{providers.ProtocolOpenAICompatible, 105, 80, 5, 10, 115, 20},
		{providers.ProtocolGemini, 100, 80, 0, 10, 110, 20},
	} {
		tokens, available, err := roundTokensFromAudit(map[string]any{"protocol": tc.protocol, "promptTokens": tc.input, "cacheReadTokens": tc.read, "cacheWriteTokens": tc.write, "completionTokens": tc.output, "totalTokens": tc.total})
		if err != nil || !available || tokens.Input != int64(tc.want) || tokens.Total != int64(tc.total) {
			t.Fatalf("%s: %+v %t %v", tc.protocol, tokens, available, err)
		}
	}
	if _, available, err := roundTokensFromAudit(map[string]any{"promptTokens": 0, "totalTokens": 0}); err != nil || available {
		t.Fatal("missing usage became measured zero")
	}
	if _, _, err := roundTokensFromAudit(map[string]any{"promptTokens": -1}); err == nil {
		t.Fatal("negative usage accepted")
	}
	f := newAgentSaveArtifactsFixture(t)
	f.server.runtimeStore = runtimekv.New(filepath.Join(t.TempDir(), "runtime.sqlite"))
	_, err := f.server.runtimeStore.Set(sessionRunnerModelAuditRuntimeNamespace, runtimeKeyFromSessionID(f.stream.SessionID)+"-1-corrupt", map[string]any{"sessionId": f.stream.SessionID, "attempt": f.claim.Attempt, "promptTokens": -1})
	if err != nil {
		t.Fatal(err)
	}
	finishRoundPresentation(t, f, "completed")
	summaries, err := f.server.completedRoundSummaries(context.Background(), f.stream.UID, f.stream.OwnerID, f.stream.SessionID, "", []int64{f.claim.Attempt})
	if err != nil || summaries[f.claim.Attempt].UsageState != "unavailable" || summaries[f.claim.Attempt].Tokens != nil {
		t.Fatalf("telemetry corrupted task: %+v %v", summaries, err)
	}
}
