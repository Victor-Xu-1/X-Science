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
	"time"

	"x-science/internal/agentruntime"
)

const languageAdvisoryOriginal = "The measured result is 12.5 nm in result.csv at https://example.test/result?a=1."

func TestFinalLanguageProviderFailureKeepsPrimaryThroughRealHTTP(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%t", streaming), func(t *testing.T) {
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				if calls.Add(1) != 1 {
					if tools, ok := request["tools"].([]any); ok && len(tools) != 0 {
						t.Error("presentation conversion acquired tools")
					}
					http.Error(w, "controlled auxiliary conversion unavailable", http.StatusBadRequest)
					return
				}
				if request["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, text := range []string{languageAdvisoryOriginal[:36], languageAdvisoryOriginal[36:]} {
						chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}})
						fmt.Fprintf(w, "data: %s\n\n", chunk)
					}
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": languageAdvisoryOriginal}, "finish_reason": "stop",
				}}})
			}))
			defer provider.Close()
			srv := newV11TestServer(t, Options{FileRoot: t.TempDir()})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			engine := srv.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{
				Endpoint: provider.URL + "/v1/chat/completions", Model: "controlled-language-provider",
				RequestTimeout: 5 * time.Second, OutputLimitBytes: 64 * 1024,
			}, nil)
			advisories := 0
			client := &sessionRunnerResponseLanguageModelClient{delegate: engine.Model, language: "zh", audit: func(record map[string]any) {
				if record["decision"] == "final_language_advisory" {
					advisories++
				}
				for _, value := range record {
					if text, ok := value.(string); ok && strings.Contains(text, "example.test") {
						t.Fatal("audit retained response payload")
					}
				}
			}}
			var emitted strings.Builder
			var response agentruntime.ModelResponse
			var err error
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "汇总已有结果。"}}}
			if streaming {
				response, err = client.CompleteStream(ctx, request, func(event agentruntime.ModelStreamEvent) error {
					emitted.WriteString(event.ContentDelta)
					return nil
				})
			} else {
				response, err = client.Complete(ctx, request)
			}
			if err != nil || !strings.HasPrefix(response.Message.Content, languageAdvisoryOriginal) ||
				!strings.Contains(response.Message.Content, "原始答复") || calls.Load() != 2 || advisories != 1 {
				t.Fatalf("primary discarded/retried or notice missing: calls=%d advisory=%d response=%q err=%v", calls.Load(), advisories, response.Message.Content, err)
			}
			if streaming && emitted.String() != response.Message.Content {
				t.Fatalf("stream and returned answer differ: emitted=%q final=%q", emitted.String(), response.Message.Content)
			}
		})
	}
}

func TestFinalLanguageInvalidConversionKeepsOriginalIdentityAndMeasuredUsage(t *testing.T) {
	for _, reply := range []agentruntime.Message{
		{Content: "已保存 99 条记录，文件为 wrong.csv。"},
		{},
		{Content: "The conversion still returns entirely English narration."},
		{Content: "转换完成。", ToolCalls: []agentruntime.ToolCall{{ID: "translator-action", Name: "write_file"}}},
	} {
		for _, streaming := range []bool{false, true} {
			model := &responseLanguageSequenceModel{
				chunks: [][]string{{languageAdvisoryOriginal}},
				responses: []agentruntime.ModelResponse{
					{Message: agentruntime.Message{Role: "assistant", Content: languageAdvisoryOriginal}, RequestID: "original-request", Model: "original-model", StopReason: "stop", Usage: agentruntime.ModelUsage{TotalTokens: 7}},
					{Message: reply, Usage: agentruntime.ModelUsage{TotalTokens: 5}},
				},
			}
			client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
			var response agentruntime.ModelResponse
			var err error
			var emitted strings.Builder
			if streaming {
				response, err = client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
					emitted.WriteString(event.ContentDelta)
					return nil
				})
			} else {
				response, err = client.Complete(context.Background(), agentruntime.ModelRequest{})
			}
			if err != nil || len(model.requests) != 3 || response.Usage.TotalTokens != 17 || len(response.Message.ToolCalls) != 0 ||
				response.RequestID != "original-request" || response.Model != "original-model" || response.StopReason != "stop" ||
				!strings.HasPrefix(response.Message.Content, languageAdvisoryOriginal) || !strings.Contains(response.Message.Content, "原始答复") {
				t.Fatalf("streaming=%t conversion=%#v calls=%d response=%#v err=%v", streaming, reply, len(model.requests), response, err)
			}
			if streaming && emitted.String() != response.Message.Content {
				t.Fatal("fallback changed or duplicated streamed primary bytes")
			}
		}
	}
}

func TestFinalLanguageSwitchAfterPublishedPrefixDoesNotReplayOrDiscard(t *testing.T) {
	const original = "答：The measured result remains 12.5 nm and the original file is result.csv."
	model := &responseLanguageSequenceModel{
		chunks:    [][]string{{"答：", original[len("答："):]}},
		responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Role: "assistant", Content: original}}},
	}
	client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
	var emitted strings.Builder
	response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		emitted.WriteString(event.ContentDelta)
		return nil
	})
	if err != nil || len(model.requests) != 1 || !strings.HasPrefix(response.Message.Content, original) ||
		!strings.Contains(response.Message.Content, "原始答复") || emitted.String() != response.Message.Content || strings.Count(emitted.String(), original) != 1 {
		t.Fatalf("published prefix replayed/discarded: calls=%d emitted=%q final=%q err=%v", len(model.requests), emitted.String(), response.Message.Content, err)
	}
}
