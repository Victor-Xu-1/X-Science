package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	workspace "x-science/internal/persistence/workspace"
)

func TestContext395RecordedCallUsagePreservesExplicitZeroAndAbsence(t *testing.T) {
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Inspect matched sample metadata."}}}
	snapshot := readProviderUsagePresenceFixture(t, false, `{"prompt_tokens":100,"completion_tokens":0,"total_tokens":100,"prompt_tokens_details":{"cached_tokens":0}}`, request)
	usage, ok := snapshot["callUsage"].(map[string]any)
	if !ok {
		t.Fatalf("successful native main call lost its typed usage: %+v", snapshot)
	}
	reported, ok := usage["reported"].(map[string]any)
	if !ok || reported["input"] != true || reported["output"] != true || reported["cache_read"] != true || reported["cache_write"] != false || usage["output_tokens"] != float64(0) {
		t.Fatalf("explicit zero/absent sibling lost: %+v", usage)
	}
	if _, present := usage["cache_read_tokens"]; present {
		t.Fatalf("the real ModelUsage omitempty zero should remain legal: %+v", usage)
	}
}

func TestContext395CallEvidenceCopyHistoryAndFailureBoundaries(t *testing.T) {
	store := runtimekv.New(filepath.Join(t.TempDir(), "context.sqlite"))
	t.Cleanup(func() { _ = store.Close() })
	recorder := &sessionContextUsageRecorder{store: store, sessionID: "recorded-details", attempt: 1, limit: 1000, limitSource: "configured"}
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Inspect matched samples."}}}
	first := recorder.begin("fixture-model", request)
	usage := agentruntime.ModelUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CacheReadTokens: 20,
		Reported:      &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true},
		InvalidFields: agentruntime.ModelUsageFields{CacheWrite: true},
		Warnings:      agentruntime.ModelUsageWarnings(0).Add(agentruntime.UsageFieldCacheWrite, agentruntime.UsageCounterType)}
	recorder.finish(first, agentruntime.ModelResponse{Usage: usage}, nil)
	usage.Reported.Input = false
	if first.CallUsage == nil || !first.CallUsage.Reported.Input {
		t.Fatal("captured receipt shares the response Reported pointer")
	}
	latest, history, _, err := readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || latest.CallUsage == nil || history.Samples[0].CallUsage == nil || !latest.CallUsage.InvalidFields.CacheWrite || latest.CallUsage.CacheReadTokens != 20 || latest.UsedTokens != 110 {
		t.Fatalf("typed siblings/history or occupancy changed: latest=%+v history=%+v err=%v", latest, history, err)
	}
	second := recorder.begin("fixture-model", request)
	unsafe := agentruntime.ModelUsage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11, CacheReadTokens: int(^uint(0) >> 1), Reported: &agentruntime.ModelUsageFields{Input: true, Output: true, Total: true}}
	recorder.finish(second, agentruntime.ModelResponse{Usage: unsafe}, nil)
	latest, history, _, err = readRunnerContextUsage(store, recorder.sessionID)
	if err != nil || latest.CallUsage != nil || latest.UsedTokens != 11 || latest.Source != "provider" || history.Samples[0].RequestID != first.RequestID || history.Samples[0].CallUsage == nil {
		t.Fatalf("unrepresentable optional metadata poisoned the old context contract: %+v %v", latest, err)
	}
	preflight := recorder.begin("fixture-model", request)
	if preflight.CallUsage != nil {
		t.Fatal("preflight fabricated completed call evidence")
	}
	recorder.finish(preflight, agentruntime.ModelResponse{Usage: usage}, errors.New("controlled failure"))
	if got := readContextUsageTest(t, store, recorder.sessionID); got.CallUsage != nil || got.State != "failed" {
		t.Fatalf("failed request fabricated completed usage: %+v", got)
	}
	if auxiliary := contextUsageRecorderForCall(withAuxiliaryContextUsage(context.Background()), recorder); auxiliary != nil {
		t.Fatal("auxiliary usage acquired the main request recorder")
	}
}

func TestContext395CallEvidenceDecoderRejectsMalformedOptionalFields(t *testing.T) {
	request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Inspect samples."}}}
	rows, _, _ := estimateRunnerRequestUsage(request)
	snapshot := runnerContextUsage{SessionID: "fixture", RequestID: "fixture-request", Model: "model", ObservedAt: time.Now(), State: "complete", Source: "provider", UsedTokens: 10, LimitTokens: 100, LimitSource: "configured", InputEstimates: rows}
	raw, _ := json.Marshal(snapshot)
	for _, invalid := range []string{`null`, `{"input_tokens":null,"output_tokens":0,"total_tokens":10}`, `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"cache_read_tokens":"bad"}`, `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"reported":{"input":null}}`, `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"cache_tokens_additive":null}`} {
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		value["callUsage"] = json.RawMessage(invalid)
		if _, err := decodeRunnerContextUsage(runtimekv.Entry{Value: value}); err == nil {
			t.Fatalf("malformed optional evidence accepted: %s", invalid)
		}
	}
}

// Exercise the actual provider decoder, dynamic main-agent recorder, temporary
// SQLite store and authenticated read API. A successful model response with a
// partial usage object is legal; absent prompt usage must not become measured 0.
func TestWebContextUsagePartialProviderUsageDoesNotClaimMeasuredTotal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{
				Role: "user", Content: strings.Repeat("abcd", 256),
			}}}
			rows, _, err := estimateRunnerRequestUsage(request)
			if err != nil {
				t.Fatal(err)
			}
			inputEstimate := 0
			for _, row := range rows {
				inputEstimate += row.Tokens
			}
			if inputEstimate <= 10 {
				t.Fatal("fixture must distinguish estimated input from the output-only receipt")
			}
			snapshot := readProviderUsagePresenceFixture(t, stream, `{"completion_tokens":10}`, request)
			if snapshot["source"] != "estimated" || snapshot["usedTokens"] != float64(inputEstimate+10) ||
				snapshot["outputTokens"] != float64(10) || snapshot["state"] != "complete" {
				t.Fatalf("partial provider receipt was mislabeled as a measured context total: inputEstimate=%d snapshot=%+v", inputEstimate, snapshot)
			}
		})
	}
}

func readProviderUsagePresenceFixture(t *testing.T, stream bool, usageJSON string, request agentruntime.ModelRequest) map[string]any {
	t.Helper()
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var received struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil || len(received.Messages) != len(request.Messages) {
			t.Errorf("legal request was not decoded: messages=%d err=%v", len(received.Messages), err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		usage := ""
		if usageJSON != "" {
			usage = `,"usage":` + usageJSON
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]%s}\n\ndata: [DONE]\n\n", usage)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]%s}`, usage)
	}))
	defer provider.Close()
	enabled := true
	srv, _, project, frame := newDynamicModelTestRuntime(t, "usage-presence-model", []workspace.ModelProviderInput{{
		ID: "usage-presence", UserID: "dynamic-user", Name: "Usage presence", Type: "openai-compatible",
		BaseURL: provider.URL + "/v1", Model: "usage-presence-model", Enabled: &enabled,
	}})
	client := newDynamicModelTestClient(srv, project, frame)
	client.contextUsage = newSessionContextUsageRecorder(srv, frame, 1, SessionRunnerChatOptions{})
	var err error
	if stream {
		_, err = client.CompleteStream(context.Background(), request, nil)
	} else {
		_, err = client.Complete(context.Background(), request)
	}
	if err != nil || calls != 1 {
		t.Fatalf("model must succeed exactly once before telemetry assertion: calls=%d err=%v", calls, err)
	}
	api := p3JSONRequest(t, srv, http.MethodGet, "/api/conversations/"+frame+"/context-usage", nil, "dynamic-user")
	if api.Code != http.StatusOK || api.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("context API HTTP=%d cache=%q body=%s", api.Code, api.Header().Get("Cache-Control"), api.Body.String())
	}
	payload := p3DecodeObject(t, api)
	snapshot, ok := payload["snapshot"].(map[string]any)
	if payload["status"] != "available" || !ok {
		t.Fatalf("context API did not return a valid persisted snapshot: %+v", payload)
	}
	if history, ok := payload["history"].(map[string]any); ok {
		samples, ok := history["samples"].([]any)
		if !ok || len(samples) == 0 || !reflect.DeepEqual(samples[len(samples)-1].(map[string]any)["callUsage"], snapshot["callUsage"]) {
			t.Fatal("authenticated latest/history call evidence does not match")
		}
	}
	return snapshot
}
