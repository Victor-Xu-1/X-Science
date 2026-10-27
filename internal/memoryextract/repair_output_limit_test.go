package memoryextract

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memorypolicy"
	"x-science/internal/providers"
)

type repairOutputReply struct {
	text, finishReason string
}

type repairOutputRequest struct {
	MaxTokens int `json:"max_tokens"`
}

func literalRepairProvider(t *testing.T, replies ...repairOutputReply) (repairModelFunc, func() []repairOutputRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []repairOutputRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input repairOutputRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Errorf("decode repair request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		index := len(requests)
		requests = append(requests, input)
		mu.Unlock()
		if index >= len(replies) {
			t.Errorf("unexpected repair request %d", index+1)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		reply := replies[index]
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "synthetic-repair", "model": "synthetic-model",
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": reply.text},
				"finish_reason": reply.finishReason,
			}},
		})
	}))
	t.Cleanup(server.Close)
	client, err := providers.NewRuntimeModelClient(providers.ModelProfile{
		Provider: providers.ProviderProfile{ID: "synthetic-provider", Protocol: providers.ProtocolOpenAICompatible, Endpoint: server.URL},
		Model:    "synthetic-model", Request: providers.RequestProfile{MaxAttempts: 1},
	}, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	model := repairModelFunc(func(ctx context.Context, request RepairRequest) (string, error) {
		temperature := request.Temperature
		response, callErr := client.Complete(ctx, agentruntime.ModelRequest{
			Messages:  []agentruntime.Message{{Role: "user", Content: request.Prompt}},
			MaxTokens: request.MaxTokens, Temperature: &temperature,
		})
		return response.Message.Content, callErr
	})
	return model, func() []repairOutputRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]repairOutputRequest(nil), requests...)
	}
}

const repairOldText = "Use v4.0.2 at /srv/project/config.json."
const repairUpdatedText = "Use v4.0.3."
const repairFullText = "Use v4.0.3 (was v4.0.2) at /srv/project/config.json."

func TestWorkspaceLiteralRepairerRegeneratesAfterTypedOutputLimit(t *testing.T) {
	model, requests := literalRepairProvider(t,
		repairOutputReply{repairFullText, "length"},
		repairOutputReply{repairFullText, "stop"},
	)
	var prompts []string
	repairer := NewModelLiteralRepairer(repairModelFunc(func(ctx context.Context, request RepairRequest) (string, error) {
		prompts = append(prompts, request.Prompt)
		return model.RepairMemoryText(ctx, request)
	}))
	result, err := repairer.RepairMemoryReplacement(context.Background(), repairOldText, repairUpdatedText)
	if err != nil || result != repairFullText {
		t.Fatalf("result=%q err=%v", result, err)
	}
	calls := requests()
	if len(calls) != 2 || calls[0].MaxTokens != memorypolicy.LiteralRepairMaxTokens || calls[1].MaxTokens != memorypolicy.ExtractionForkedMaxTokens {
		t.Fatalf("repair requests=%#v", calls)
	}
	if prompts[0] != prompts[1] {
		t.Fatal("output-limit recovery must regenerate from the same full old/update input")
	}
}

func TestWorkspaceLiteralRepairerOutputLimitRemainsBounded(t *testing.T) {
	model, requests := literalRepairProvider(t,
		repairOutputReply{repairFullText, "length"},
		repairOutputReply{repairFullText, "length"},
	)
	result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(context.Background(), repairOldText, repairUpdatedText)
	if result != "" || !errors.Is(err, ErrLiteralRepairFailed) || !providers.IsProviderOutputTokenLimit(err) || len(requests()) != memorypolicy.LiteralRepairAttempts {
		t.Fatalf("result=%q requests=%#v err=%v", result, requests(), err)
	}
}

func TestWorkspaceLiteralRepairerRecoveredOutputStillRequiresValidation(t *testing.T) {
	for name, candidate := range map[string]string{
		"invented-value":     repairFullText + " Build 987654.",
		"lost-updated-value": "Use v4.0.2 at /srv/project/config.json.",
		"utf16-limit":        repairFullText + strings.Repeat("😀", memorypolicy.TextMaxUTF16Units/2),
	} {
		t.Run(name, func(t *testing.T) {
			model, requests := literalRepairProvider(t, repairOutputReply{repairFullText, "length"}, repairOutputReply{candidate, "stop"})
			result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(context.Background(), repairOldText, repairUpdatedText)
			if result != "" || !errors.Is(err, ErrLiteralRepairFailed) || len(requests()) != 2 {
				t.Fatalf("result=%q requests=%#v err=%v", result, requests(), err)
			}
		})
	}
}

func TestWorkspaceLiteralRepairerNonLimitFailureDoesNotRetry(t *testing.T) {
	cause := errors.New("provider response truncated: finish reason length")
	calls := 0
	model := repairModelFunc(func(context.Context, RepairRequest) (string, error) {
		calls++
		return repairFullText, cause
	})
	result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(context.Background(), repairOldText, repairUpdatedText)
	if result != "" || calls != 1 || !errors.Is(err, cause) {
		t.Fatalf("result=%q calls=%d err=%v", result, calls, err)
	}
	t.Run("nil-context", func(t *testing.T) {
		calls := 0
		repairer := NewModelLiteralRepairer(repairModelFunc(func(context.Context, RepairRequest) (string, error) {
			calls++
			return repairFullText, nil
		}))
		result, err := repairer.RepairMemoryReplacement(nil, repairOldText, repairUpdatedText)
		if result != "" || calls != 0 || !errors.Is(err, ErrLiteralRepairFailed) {
			t.Fatalf("nil-context result=%q calls=%d err=%v", result, calls, err)
		}
		result, err = repairer.RepairMemoryReplacement(nil, "stable text", "updated stable text")
		if result != "updated stable text" || err != nil || calls != 0 {
			t.Fatalf("no-repair nil-context result=%q calls=%d err=%v", result, calls, err)
		}
	})
}

func TestWorkspaceLiteralRepairerKeepsOneDeadlineAndCancelCause(t *testing.T) {
	parentDeadline := time.Now().Add(time.Minute)
	bounded, release := context.WithDeadline(context.Background(), parentDeadline)
	defer release()
	parent, cancel := context.WithCancelCause(bounded)
	cause := errors.New("synthetic owner cancelled")
	defer cancel(cause)
	var deadlines []time.Time
	calls := 0
	model := repairModelFunc(func(ctx context.Context, _ RepairRequest) (string, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || !deadline.Equal(parentDeadline) {
			t.Fatal("repair extended or lost the caller's overall deadline")
		}
		deadlines = append(deadlines, deadline)
		if calls == 1 {
			return "Use v4.0.3.", nil
		}
		cancel(cause)
		return "", context.Canceled
	})
	result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(parent, repairOldText, repairUpdatedText)
	if result != "" || calls != 2 || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("result=%q calls=%d deadlines=%v err=%v", result, calls, deadlines, err)
	}
	cancelledCalls := 0
	result, err = NewModelLiteralRepairer(repairModelFunc(func(context.Context, RepairRequest) (string, error) {
		cancelledCalls++
		return repairFullText, nil
	})).RepairMemoryReplacement(parent, repairOldText, repairUpdatedText)
	if result != "" || cancelledCalls != 0 || !errors.Is(err, cause) {
		t.Fatalf("already-cancelled result=%q calls=%d err=%v", result, cancelledCalls, err)
	}
}

func TestWorkspaceLiteralRepairerCancelledOutputLimitDoesNotRetry(t *testing.T) {
	provider, requests := literalRepairProvider(t, repairOutputReply{repairFullText, "length"})
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("synthetic cancelled owner")
	defer cancel(cause)
	model := repairModelFunc(func(callCtx context.Context, request RepairRequest) (string, error) {
		text, err := provider.RepairMemoryText(callCtx, request)
		cancel(cause)
		return text, err
	})
	result, err := NewModelLiteralRepairer(model).RepairMemoryReplacement(ctx, repairOldText, repairUpdatedText)
	if result != "" || len(requests()) != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("result=%q requests=%#v err=%v", result, requests(), err)
	}
}
