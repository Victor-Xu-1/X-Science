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
)

func TestResponseLanguageLiteralFinalTraversesHTTPAndDurableCompletion(t *testing.T) {
	const frameID, content = "literal-response-frame", "TRACE-CHECK-a18f439b：2+2=4"
	store, repo, _ := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "literal-response-project", frameID)
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: frameID, MessageUUID: "literal-input-message", ClientMessageID: "literal-input",
		Text: "部署技术烟测 a18f439b：这是独立的界面发送与最终交付检查，不是科研任务。请直接回复“" + content + "”。不需要计划、工具调用、联网、创建文件或额外核验。",
	}); err != nil {
		t.Fatal(err)
	}
	seedAnsweredTaskIntake(t, server, "local", frameID)
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid protocol request", http.StatusBadRequest)
			return
		}
		if strings.Contains(fmt.Sprint(request["messages"]), "Write a brief expert orientation before the task begins.") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "按要求原样返回指定内容。"},
			}}})
			return
		}
		if requests.Add(1) != 1 {
			http.Error(w, "literal final must not acquire a conversion round", http.StatusConflict)
			return
		}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"role": "assistant", "content": content},
			}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}}})
	}))
	defer provider.Close()
	result, err := server.RunSessionRunnerChatOnce(context.Background(), SessionRunnerChatOptions{
		SessionID: frameID, RunnerID: "literal-protocol-runner", Endpoint: provider.URL + "/v1/chat/completions",
		APIKey: "local-test", Model: "local-test", LeaseTTL: time.Minute, MaxAttempts: 1,
		DisableSkillDiscovery: true, DisableMCPDiscovery: true,
	})
	if err != nil || result.Status != "completed" || requests.Load() != 1 || result.FinishEventID == 0 {
		t.Fatalf("literal final was not committed: result=%#v requests=%d error=%v", result, requests.Load(), err)
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", frameID)
	if err != nil || !found {
		t.Fatalf("stream: found=%t error=%v", found, err)
	}
	terminal, err := repo.GetTerminalProjection(context.Background(), stream.OwnerID, stream.UID, result.FinishEventID)
	if err != nil || terminal.TerminalStatus != "completed" || terminal.ReasonCode != "" {
		t.Fatalf("terminal=%#v error=%v", terminal, err)
	}
	history := compatJSONRequest(t, server.Handler(), http.MethodGet, "/api/conversations/"+frameID+"/messages?limit=10", "local", nil, http.StatusOK)
	answers := 0
	for _, item := range anySliceValue(history["items"]) {
		message := mapValue(item)
		if message["position"] != "left" {
			continue
		}
		answers++
		if message["terminal_status"] != "completed" || mapValue(message["content"])["content"] != content ||
			numberValue(mapValue(message["round_summary"])["call_count"]) != 1 {
			t.Fatalf("normal history lost exact final or call bound: %#v", message)
		}
	}
	if answers != 1 {
		t.Fatalf("published %d answers instead of exactly one", answers)
	}
}
