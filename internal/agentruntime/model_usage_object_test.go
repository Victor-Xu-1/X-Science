package agentruntime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func directUsageObject(raw string) string {
	return `{"id":"usage-object","model":"fixture","choices":[{"message":{"role":"assistant","content":"done","tool_calls":[{"id":"call-usage","type":"function","function":{"name":"lookup","arguments":"{\"term\":\"record\"}"}}]},"finish_reason":"tool_calls"}],"usage":` + raw + `}`
}

func directUsageObjectCounts(response openAIChatResponse) ModelUsage {
	usage := ModelUsageFromCounters(response.Usage.PromptTokens, response.Usage.CompletionTokens,
		response.Usage.TotalTokens, response.Usage.PromptTokensDetails.CachedTokens, OptionalUsageCounter{})
	usage.DeriveTotal(false)
	return usage
}

func TestOptionalUsageObjectsClassifyShapeAndResetReceiver(t *testing.T) {
	for _, raw := range []string{`[]`, `"private-usage-junk"`, `true`, `17`} {
		for _, nested := range []bool{false, true} {
			t.Run(raw+map[bool]string{false: "/usage", true: "/details"}[nested], func(t *testing.T) {
				wire := raw
				wantInvalid := ModelUsageFields{Input: true, Output: true, Total: true, CacheRead: true}
				if nested {
					wire = `{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23,"prompt_tokens_details":` + raw + `}`
					wantInvalid = ModelUsageFields{CacheRead: true}
				}
				var response openAIChatResponse
				if err := json.Unmarshal([]byte(directUsageObject(wire)), &response); err != nil {
					t.Fatalf("optional object shape rejected primary completion: %v", err)
				}
				usage := directUsageObjectCounts(response)
				if usage.InvalidFields != wantInvalid || usage.Warnings.Empty() || usage.Reported.CacheRead || usage.InvalidFields.CacheWrite {
					t.Fatalf("invalid object did not retain supported-field boundaries: %+v", usage)
				}
				if nested && (usage.InputTokens != 20 || usage.OutputTokens != 3 || usage.TotalTokens != 23 || !usage.Reported.Total) {
					t.Fatal("nested details erased valid base counters or independent total")
				}
				for _, reset := range []string{`null`, `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}`} {
					if err := json.Unmarshal([]byte(directUsageObject(reset)), &response); err != nil {
						t.Fatal(err)
					}
					usage = directUsageObjectCounts(response)
					if usage.InvalidFields != (ModelUsageFields{}) || !usage.Warnings.Empty() || usage.TotalTokens != 0 ||
						usage.Reported.Input != (reset != "null") || usage.Reported.CacheRead != (reset != "null") {
						t.Fatalf("receiver retained prior telemetry across null/valid reset: %+v", usage)
					}
				}
			})
		}
	}
	var absent openAIChatResponse
	if err := json.Unmarshal([]byte(`{"id":"usage-object","choices":[{"message":{"role":"assistant","content":"done"}}]}`), &absent); err != nil {
		t.Fatal(err)
	}
	if usage := directUsageObjectCounts(absent); usage.ReportedFields() != (ModelUsageFields{}) || !usage.Warnings.Empty() {
		t.Fatal("absent usage became a zero measurement or warning")
	}
	for _, raw := range []string{`{"usage":{"prompt_tokens":`, `{"usage":[],"id":17}`} {
		if err := json.Unmarshal([]byte(raw), &openAIChatResponse{}); err == nil {
			t.Fatal("malformed envelope was softened")
		}
	}
	var details openAIChatResponse
	for _, raw := range []string{`false`, `{"cached_tokens":0}`, `null`} {
		if err := json.Unmarshal([]byte(raw), &details.Usage.PromptTokensDetails); err != nil {
			t.Fatal(err)
		}
	}
	if details.Usage.PromptTokensDetails.CachedTokens.State() != UsageCounterMissing {
		t.Fatal("null nested receiver retained prior measured zero")
	}
}

func TestOptionalUsageObjectHelperKeepsSyntaxAndTypedErrors(t *testing.T) {
	for _, raw := range []string{"", "   ", `{"count":`, `[}`, `{"name":17}`} {
		var target struct {
			Count OptionalUsageCounter `json:"count"`
			Name  string               `json:"name"`
		}
		if err := UnmarshalOptionalUsageObject([]byte(raw), &target, &target.Count); err == nil {
			t.Fatalf("optional object helper softened malformed JSON or typed object: %q", raw)
		}
	}
}

func TestDirectOpenAIUsageObjectsPreservePrimaryAndToolIdentity(t *testing.T) {
	for _, mode := range []string{"usage", "details", "primary"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			body := directUsageObject(`["private-usage-junk"]`)
			if mode == "details" {
				body = directUsageObject(`{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23,"prompt_tokens_details":false}`)
			}
			if mode == "primary" {
				body = strings.Replace(body, `"id":"usage-object"`, `"id":17`, 1)
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer provider.Close()
			client := OpenAIChatClient{Endpoint: provider.URL, Model: "fixture", HTTPClient: provider.Client(), MaxAttempts: 2}
			response, err := client.Complete(t.Context(), ModelRequest{})
			if calls.Load() != 1 {
				t.Fatalf("usage object caused an extra request: %d", calls.Load())
			}
			if mode == "primary" {
				var typeError *json.UnmarshalTypeError
				if !errors.As(err, &typeError) || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
					t.Fatalf("partially decoded primary response was used: %v", err)
				}
				return
			}
			if err != nil || response.Message.Content != "done" || response.RequestID != "usage-object" || response.Model != "fixture" ||
				response.StopReason != "tool_calls" || len(response.Message.ToolCalls) != 1 {
				t.Fatalf("valid primary response changed: %+v err=%v", response, err)
			}
			call := response.Message.ToolCalls[0]
			if call.ID != "call-usage" || call.Name != "lookup" || string(call.Arguments) != `{"term":"record"}` {
				t.Fatalf("usage changed tool identity or arguments: %+v", call)
			}
			encoded, err := json.Marshal(response.Usage)
			if err != nil || strings.Contains(string(encoded), "private-usage-junk") || response.Usage.Warnings.Empty() {
				t.Fatal("usage diagnostics leaked raw junk or disappeared")
			}
		})
	}
}
