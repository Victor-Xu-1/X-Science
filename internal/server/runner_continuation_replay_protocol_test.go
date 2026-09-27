package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	"synon-go/internal/providers"
)

func TestContinuationTruncatedTailReplayUsesRealStream(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, text, finish string
		noProgress                 bool
	}{
		{"tail", "An observation: echo", "echo", "length", true},
		{"unicode", "状态：待", "待", "length", true},
		{"whole-prefix", "saved text", "saved text", "length", true},
		{"unfinished-tool", "An observation: echo", "echo", "tool-length", true},
		{"new-content", "An observation: echo", "echo followed by new evidence", "length", false},
		{"legitimate-repeat", "An observation: echo", "echo", "stop", false},
		{"different-short", "An observation: echo", "x", "length", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, char := range tc.text {
					encoded, _ := json.Marshal(string(char))
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", encoded)
					w.(http.Flusher).Flush()
				}
				finish := tc.finish
				if finish == "tool-length" {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"action-1\",\"type\":\"function\",\"function\":{\"name\":\"inspect\",\"arguments\":\"{\"}}]}}]}\n\n")
					finish = "length"
				}
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\ndata: [DONE]\n\n", finish)
			}))
			defer provider.Close()
			srv := New(Options{FileRoot: t.TempDir()})
			engine := srv.newAgentRuntimeEngine(SessionRunnerChatOptions{
				Endpoint: provider.URL, Model: "continuation-test", RequestTimeout: time.Second, MaxAttempts: 1,
			})
			client := &sessionRunnerContinuationModelClient{delegate: engine.Model, prefix: tc.prefix}
			var visible strings.Builder
			_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Continue the saved work."}},
			}, func(event agentruntime.ModelStreamEvent) error {
				visible.WriteString(event.ContentDelta)
				return nil
			})
			var stalled sessionRunnerProviderNoProgressInterruption
			if tc.noProgress {
				if !errors.As(err, &stalled) || visible.Len() != 0 {
					t.Fatalf("truncated replay counted as progress: visible=%q err=%v", visible.String(), err)
				}
			} else if visible.String() != tc.text || errors.As(err, &stalled) ||
				(tc.finish == "stop" && err != nil) || (tc.finish == "length" && !providers.IsContinuationSafeResponseTruncation(err)) {
				t.Fatalf("new or completed content lost: visible=%q err=%v", visible.String(), err)
			}
		})
	}
}

func TestContinuationContextIsRequestScopedWithRealProvider(t *testing.T) {
	var mu sync.Mutex
	var requests [][]agentruntime.Message
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		requests = append(requests, request.Messages)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"new observation\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	srv := New(Options{FileRoot: t.TempDir()})
	engine := srv.newAgentRuntimeEngine(SessionRunnerChatOptions{Endpoint: provider.URL, Model: "scope-test", RequestTimeout: time.Second, MaxAttempts: 1})
	state := &sessionRunnerProviderContinuationState{}
	state.Content.WriteString("saved draft")
	client := &sessionRunnerContinuationModelClient{delegate: engine.Model, prefix: state.Content.String(),
		contextMessages: agentRuntimeMessagesFromChat(appendProviderContinuationContext(nil, state))}
	base := make([]agentruntime.Message, 1, 8)
	base[0] = agentruntime.Message{Role: "user", Content: "Continue the task."}
	for i := 0; i < 2; i++ {
		if _, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{Messages: base}, nil); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || len(requests[0]) != 3 || len(requests[1]) != 1 || requests[0][1].Content != "saved draft" {
		t.Fatalf("continuation authority leaked across requests: %#v", requests)
	}
	if base[:cap(base)][1].Role != "" {
		t.Fatal("request mutated the engine's backing history")
	}
}

func TestContinuationTailProbePreservesBoundariesAndCancellation(t *testing.T) {
	for _, mode := range []string{"tool", "public", "cancel", "callback-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []agentruntime.ModelStreamEvent
			sentinel := errors.New("publication failed")
			probe := newContinuationTailProbe("saved tail", func(e agentruntime.ModelStreamEvent) error {
				events = append(events, e)
				if mode == "callback-error" {
					return sentinel
				}
				return nil
			})
			if err := probe.event(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "tail"}); err != nil {
				t.Fatal(err)
			}
			kind := agentruntime.ModelStreamEventToolCallBoundary
			if mode == "public" {
				kind = agentruntime.ModelStreamEventPublicProgressDelta
			}
			if mode == "cancel" {
				client := &sessionRunnerContinuationModelClient{prefix: "saved tail", delegate: cancellingContinuationFixture{cancel: cancel}}
				_, err := client.CompleteStream(ctx, agentruntime.ModelRequest{}, func(e agentruntime.ModelStreamEvent) error { events = append(events, e); return nil })
				if !errors.Is(err, context.Canceled) || len(events) != 0 {
					t.Fatalf("cancel published pending text: %v %#v", err, events)
				}
				return
			}
			err := probe.event(agentruntime.ModelStreamEvent{Kind: kind})
			if err == nil {
				err = probe.flush()
			}
			if mode == "callback-error" {
				if !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || len(events) != 2 || events[0].ContentDelta != "tail" || events[1].Kind != kind || probe.onlyReplayedTail(false) {
				t.Fatalf("boundary lost: %v %#v", err, events)
			}
		})
	}
}

type cancellingContinuationFixture struct{ cancel context.CancelFunc }

func (c cancellingContinuationFixture) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	panic("stream only")
}
func (c cancellingContinuationFixture) CompleteStream(_ context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "tail"}); err != nil {
		return agentruntime.ModelResponse{}, err
	}
	c.cancel()
	return agentruntime.ModelResponse{}, context.Canceled
}
