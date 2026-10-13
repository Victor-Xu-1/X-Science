package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

func TestOutputBudgetSilentSavedAllowanceProbesDefaultAcrossRecreation(t *testing.T) {
	for _, protocol := range []string{providers.ProtocolOpenAICompatible, providers.ProtocolAnthropic} {
		t.Run(protocol, func(t *testing.T) {
			f := newOutputProbeFixture(t, protocol, func(w http.ResponseWriter, budget int) {
				if budget == 17 {
					writeProbeLimit(w, protocol, "private", 17)
				} else {
					writeProbeSuccess(w, protocol)
				}
			})
			if _, err := f.call(t, agentruntime.ModelRequest{}); !providers.IsProviderOutputTokenLimit(err) {
				t.Fatalf("initial length stop lost: %v", err)
			}
			f.reopen(t)
			for range 2 {
				response, err := f.call(t, agentruntime.ModelRequest{})
				if err != nil || response.Message.Content != "ready" {
					t.Fatalf("default probe response=%+v err=%v", response, err)
				}
				f.reopen(t)
			}
			budgets := f.budgets(3)
			if protocol == providers.ProtocolOpenAICompatible {
				if !reflect.DeepEqual(budgets, []int{17, 0, 0}) {
					t.Fatalf("silent output retried a small saved allowance: %v", budgets)
				}
			} else if budgets[0] != 17 || budgets[1] <= 0 || budgets[1] == 34 || budgets[2] != budgets[1] {
				t.Fatalf("Anthropic lost its required default max_tokens: %v", budgets)
			}
			if *f.profile.MaxTokens != 17 {
				t.Fatal("recovery mutated saved profile allowance")
			}
		})
	}
}

func TestOutputBudgetDefaultProbeDoesNotOverrideHardLimitOrReplayProgress(t *testing.T) {
	for _, kind := range []string{"hard-limit", "content", "tool"} {
		t.Run(kind, func(t *testing.T) {
			f := newOutputProbeFixture(t, providers.ProtocolOpenAICompatible, func(w http.ResponseWriter, budget int) {
				writeProbeLimit(w, providers.ProtocolOpenAICompatible, kind, budget)
			})
			request := agentruntime.ModelRequest{}
			if kind == "hard-limit" {
				request.MaxTokens = 7
			}
			for range 2 {
				response, err := f.call(t, request)
				if !providers.IsProviderOutputTokenLimit(err) || len(response.Message.ToolCalls) != 0 {
					t.Fatalf("partial action escaped boundary: response=%+v err=%v", response, err)
				}
			}
			want := []int{17, 34}
			if kind == "hard-limit" {
				want = []int{7, 7}
			}
			if got := f.budgets(2); !reflect.DeepEqual(got, want) {
				t.Fatalf("budget=%v want=%v", got, want)
			}
			if kind == "content" && f.content != "prefixprefix" {
				t.Fatalf("accepted native prefix was lost: %q", f.content)
			}
		})
	}
}

func TestOutputBudgetFailedDefaultProbeReturnsToBoundedNegotiation(t *testing.T) {
	for _, failure := range []string{"length", "http", "typed-maximum"} {
		t.Run(failure, func(t *testing.T) {
			f := newOutputProbeFixture(t, providers.ProtocolOpenAICompatible, func(w http.ResponseWriter, budget int) {
				if budget == 0 && failure == "http" {
					http.Error(w, "temporary fixture failure", http.StatusServiceUnavailable)
					return
				}
				if budget == 0 && failure == "typed-maximum" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"param":"max_tokens","message":"The parameter max_tokens is invalid: expected a value <= 23."}}`)
					return
				}
				writeProbeLimit(w, providers.ProtocolOpenAICompatible, "private", 17)
			})
			for range 4 {
				_, err := f.call(t, agentruntime.ModelRequest{})
				if err == nil {
					t.Fatal("failed generation was presented as successful")
				}
				f.reopen(t)
			}
			budgets := f.budgets(4)
			want := []int{17, 0, 34, 25}
			if failure == "typed-maximum" {
				want = []int{17, 0, 23, 20}
			}
			if !reflect.DeepEqual(budgets, want) {
				t.Fatalf("probe repeated or negotiation lost its bounds: %v want %v", budgets, want)
			}
		})
	}
}

type outputProbeFixture struct {
	profile  providers.ModelProfile
	store    *runtimekv.Store
	path     string
	api      *httptest.Server
	requests chan int
	content  string
}

func newOutputProbeFixture(t *testing.T, protocol string, reply func(http.ResponseWriter, int)) *outputProbeFixture {
	t.Helper()
	f := &outputProbeFixture{requests: make(chan int, 16), path: filepath.Join(t.TempDir(), "runtime.sqlite")}
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		f.requests <- request.MaxTokens
		reply(w, request.MaxTokens)
	}))
	t.Cleanup(f.api.Close)
	limit := 17
	f.profile = providers.ModelProfile{Provider: providers.ProviderProfile{ID: "probe-provider", UserID: "owner", Protocol: protocol, Endpoint: f.api.URL}, Model: "model", MaxTokens: &limit, Request: providers.RequestProfile{MaxAttempts: 1}}
	f.store = runtimekv.New(f.path)
	t.Cleanup(func() { _ = f.store.Close() })
	return f
}

func (f *outputProbeFixture) call(t *testing.T, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	t.Helper()
	delegate, err := providers.NewRuntimeModelClient(f.profile, f.api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newSessionOutputBudgetClient(delegate, f.store, f.profile, "task", "agent").(agentruntime.StreamingModelClient)
	return client.CompleteStream(context.Background(), request, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind != agentruntime.ModelStreamEventPrivateReasoning {
			f.content += event.ContentDelta
		}
		return nil
	})
}

func (f *outputProbeFixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store = runtimekv.New(f.path)
}

func (f *outputProbeFixture) budgets(count int) []int {
	result := make([]int, count)
	for i := range result {
		result[i] = <-f.requests
	}
	return result
}

func writeProbeLimit(w http.ResponseWriter, protocol, kind string, tokens int) {
	w.Header().Set("Content-Type", "text/event-stream")
	if protocol == providers.ProtocolAnthropic {
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":100}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"private fixture\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":%d}}\n\n", tokens)
		return
	}
	delta := map[string]any{"reasoning_content": "private fixture"}
	if kind == "content" {
		delta = map[string]any{"content": "prefix"}
	}
	if kind == "tool" {
		delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "partial", "function": map[string]any{"name": "read_file", "arguments": "{"}}}}
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "length"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": tokens}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

func writeProbeSuccess(w http.ResponseWriter, protocol string) {
	w.Header().Set("Content-Type", "application/json")
	if protocol == providers.ProtocolAnthropic {
		fmt.Fprint(w, `{"id":"m","role":"assistant","content":[{"type":"text","text":"ready"}],"stop_reason":"end_turn"}`)
	} else {
		fmt.Fprint(w, strings.TrimSpace(`{"choices":[{"message":{"role":"assistant","content":"ready"},"finish_reason":"stop"}]}`))
	}
}
