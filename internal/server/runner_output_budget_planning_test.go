package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

func TestOutputBudgetPlanningDeadlineRetiresFailedProbeFallbackAcrossRecreation(t *testing.T) {
	for _, native := range []string{"none", "content", "partial-tool"} {
		t.Run(native, func(t *testing.T) {
			var recovered atomic.Bool
			f := &outputProbeFixture{requests: make(chan int, 8), path: filepath.Join(t.TempDir(), "runtime.sqlite")}
			f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				f.requests <- request.MaxTokens
				if recovered.Load() {
					writeProbeSuccess(w, providers.ProtocolOpenAICompatible)
					return
				}
				if request.MaxTokens != 0 {
					writeProbeLimit(w, providers.ProtocolOpenAICompatible, "private", request.MaxTokens)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if native == "content" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted prefix\"}}]}\n\n")
					w.(http.Flusher).Flush()
				} else if native == "partial-tool" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"partial-call\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\"}}]}}]}\n\n")
					w.(http.Flusher).Flush()
				}
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				deadline := time.NewTimer(2 * time.Second)
				defer deadline.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-deadline.C:
						return
					case <-ticker.C:
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
						w.(http.Flusher).Flush()
					}
				}
			}))
			t.Cleanup(f.api.Close)
			limit := 17
			f.profile = providers.ModelProfile{Provider: providers.ProviderProfile{ID: "planning", UserID: "owner", Protocol: providers.ProtocolOpenAICompatible, Endpoint: f.api.URL}, Model: "model", MaxTokens: &limit, Request: providers.RequestProfile{MaxAttempts: 1, Timeout: 100 * time.Millisecond}}
			f.store = runtimekv.New(f.path)
			t.Cleanup(func() { _ = f.store.Close() })
			request := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "read_file", Parameters: map[string]any{"type": "object"}}}}
			if _, err := f.call(t, request); !providers.IsProviderOutputTokenLimit(err) {
				t.Fatalf("initial saved allowance did not produce typed length: %v", err)
			}
			f.reopen(t)
			response, err := f.call(t, request)
			if !providers.IsProviderPlanningNoProgress(err) || providers.IsProviderEmptyResponse(err) != (native != "content") ||
				providers.IsProviderPlanningNoNativeProgress(err) != (native == "none") || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("planning deadline lost typed visibility boundary: %v", err)
			}
			f.reopen(t)
			client := newSessionOutputBudgetClient(nil, f.store, f.profile, "task", "agent").(*sessionOutputBudgetClient)
			state, err := client.read()
			wantNext := 0
			if native != "none" {
				wantNext = 34
			}
			if err != nil || state.Next != wantNext || state.Lower != 17 || state.Ceiling != 0 || state.Rejected != 0 || !state.DefaultProbeUsed || state.DefaultProbePending || !state.SkipInitialAllowance {
				t.Fatalf("planning failure changed measured bounds or retained failed hint: %+v err=%v", state, err)
			}
			if _, found, err := f.store.Get(client.preferenceNamespace, "preference"); err != nil || found {
				t.Fatalf("failed planning taught a successful cross-task preference: %t %v", found, err)
			}
			if native != "none" {
				wantContent := ""
				if native == "content" {
					wantContent = "accepted prefix"
				}
				if f.content != wantContent {
					t.Fatalf("accepted stream prefix was changed or private content leaked: %q", f.content)
				}
				return
			}
			// Explicitly invoking the recovered client models a user-authorized retry;
			// the canonical runner test separately verifies unattended dispatch stops.
			recovered.Store(true)
			if response, err := f.call(t, request); err != nil || response.Message.Content != "ready" {
				t.Fatalf("manual continuation was permanently frozen: %+v %v", response, err)
			}
			request.MaxTokens = 7
			if _, err := f.call(t, request); err != nil {
				t.Fatal(err)
			}
			if got := f.budgets(4); !reflect.DeepEqual(got, []int{17, 0, 0, 7}) {
				t.Fatalf("failed probe replayed old growth or overrode hard budget: %v", got)
			}
			// The successful JSON fixture returns its text in ModelResponse,
			// not as a streamed delta. No private text may reach the callback.
			if f.content != "" {
				t.Fatalf("private content leaked: %q", f.content)
			}
			if *f.profile.MaxTokens != 17 {
				t.Fatal("saved configuration was rewritten")
			}
		})
	}
}
