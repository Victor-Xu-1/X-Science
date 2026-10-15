package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/workspace"
)

func TestFinalLanguageBoundConversionKeepsDurableMainContextUsage(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("conversion_failure=%t", failure), func(t *testing.T) {
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []agentruntime.Message `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				call := calls.Add(1)
				content, tokens := "Saved 2 records in sample.cif.", 500
				if call == 2 {
					content, tokens = "已保存 9 条记录，文件为 wrong.cif。", 100
				} else if call == 3 {
					if failure {
						http.Error(w, "controlled bound conversion unavailable", http.StatusBadRequest)
						return
					}
					text := request.Messages[len(request.Messages)-1].Content
					var masked string
					if err := json.Unmarshal([]byte(text[strings.IndexByte(text, '\n')+1:]), &masked); err != nil {
						t.Error(err)
						http.Error(w, "invalid template", http.StatusBadRequest)
						return
					}
					content = strings.NewReplacer("Saved ", "已保存 ", " records in ", " 条记录，文件为 ").Replace(masked)
					tokens = 50
				} else if call > 3 {
					t.Error("unexpected conversion retry")
					http.Error(w, "too many calls", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
					"usage":   map[string]any{"prompt_tokens": tokens - 10, "completion_tokens": 10, "total_tokens": tokens},
				})
			}))
			defer provider.Close()
			enabled := true
			srv, _, project, frame := newDynamicModelTestRuntime(t, "observed-model", []workspace.ModelProviderInput{{
				ID: "observed", UserID: "dynamic-user", Name: "Observed", Type: "openai-compatible",
				BaseURL: provider.URL + "/v1", Model: "observed-model", Enabled: &enabled,
			}})
			dynamic := newDynamicModelTestClient(srv, project, frame)
			options := SessionRunnerChatOptions{RuntimeSessionConfig: map[string]any{"contextWindow": 500000}}
			dynamic.contextOptions = options
			dynamic.contextUsage = newSessionContextUsageRecorder(srv, frame, 1, options)
			client := &sessionRunnerResponseLanguageModelClient{delegate: dynamic, language: "zh"}
			response, err := client.Complete(context.Background(), agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "汇总已有结果"}}})
			expectedContent, expectedTokens := "已保存 2 条记录，文件为 sample.cif.", 650
			if failure {
				expectedContent, expectedTokens = "Saved 2 records in sample.cif.\n\n> "+finalLanguageNoticeChinese, 600
			}
			// Inspect the real durable recorder, not only a context marker:
			// the short second conversion must never replace the main window.
			got := readContextUsageTest(t, srv.runtimeStore, frame)
			if got.Source != "provider" || got.UsedTokens != 500 || got.State != "complete" || got.Model != "observed-model" {
				t.Fatalf("bound conversion replaced main request: snapshot=%+v calls=%d final_err=%v", got, calls.Load(), err)
			}
			if err != nil || calls.Load() != 3 || response.Usage.TotalTokens != expectedTokens || response.Message.Content != expectedContent {
				t.Fatalf("bound conversion outcome/usage lost: response=%#v calls=%d err=%v", response, calls.Load(), err)
			}
		})
	}
}
