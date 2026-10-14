package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDurableToolCallIdentitySurvivesFreshRunAndCompactedHistory(t *testing.T) {
	var executed []ToolCall
	var persisted []ToolCall
	engine := Engine{
		ToolCallIdentityPolicy: DurableToolCallIdentity,
		Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
			executed = append(executed, call)
			return ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
		OnEventError: func(event Event) error {
			if event.Type == EventModelResponse {
				persisted = append(persisted, event.ToolCalls...)
			}
			return nil
		},
	}
	seen := map[string]bool{}
	for _, rawID := range []string{"call_0", "call_0", strings.Repeat("x", 512)} {
		client := &capturingRequestModelClient{responses: []ModelResponse{
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: rawID, Name: "noop", Arguments: json.RawMessage(`{}`)}}}},
			{Message: Message{Role: "assistant", Content: "done"}},
		}}
		engine.Model = client
		result, err := engine.Run(context.Background(), RunRequest{Messages: []Message{
			{Role: "system", Content: "Compacted previous observations."}, {Role: "user", Content: "Continue."},
		}, Tools: []ToolSchema{{Name: "noop"}}})
		if err != nil || len(executed) != len(persisted) || len(executed) == 0 {
			t.Fatalf("run=%#v err=%v executed=%#v persisted=%#v", result, err, executed, persisted)
		}
		id := executed[len(executed)-1].ID
		if id == rawID || seen[id] || len(id) > 512 || id != persisted[len(persisted)-1].ID {
			t.Fatalf("new durable identity is not fresh/persisted/bounded: raw=%q canonical=%q", rawID, id)
		}
		seen[id] = true
		if result.Messages[2].ToolCalls[0].ID != id || result.Messages[3].ToolCallID != id ||
			client.requests[1].Messages[2].ToolCalls[0].ID != id || client.requests[1].Messages[3].ToolCallID != id {
			t.Fatalf("assistant/tool protocol pairing lost canonical identity %q", id)
		}
	}
	// Replaying a completed batch uses its persisted cursor and cannot allocate
	// identities or repeat an already completed side effect.
	batch, err := engine.ExecuteToolBatch(context.Background(), persisted, len(persisted), MediaPolicy{}, 0)
	if err != nil || batch.NextOrdinal != len(persisted) || len(executed) != len(persisted) {
		t.Fatalf("completed replay=%#v err=%v executions=%d", batch, err, len(executed))
	}
	// Executing a pending durable item keeps its stored ID, even when this
	// Engine's policy would assign a fresh ID to a new model response.
	pending := ToolCall{ID: "legacy-persisted-pending", Name: "noop", Arguments: json.RawMessage(`{}`)}
	batch, err = engine.ExecuteToolBatch(context.Background(), []ToolCall{pending}, 0, MediaPolicy{}, 0)
	if err != nil || batch.Messages[0].ToolCallID != pending.ID || executed[len(executed)-1].ID != pending.ID {
		t.Fatalf("pending replay changed durable identity: batch=%#v err=%v", batch, err)
	}
}

func TestDurableToolCallIdentityDoesNotTrustProviderCanonicalLookingIDs(t *testing.T) {
	scope, err := newToolCallIdentityScope(DurableToolCallIdentity)
	if err != nil {
		t.Fatal(err)
	}
	first, err := scope.namespace(nil, []ToolCall{{ID: "call_0"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scope.namespace(nil, []ToolCall{{ID: first[0].ID}}, 1)
	if err != nil || first[0].ID == second[0].ID {
		t.Fatalf("model selected a prior canonical ID: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestDurableToolCallIdentityPublishesOnlyCanonicalStreamLifecycle(t *testing.T) {
	boundaryObserved := false
	var publishedID string
	var completedID string
	executions := 0
	engine := Engine{
		ToolCallIdentityPolicy: DurableToolCallIdentity,
		Model: &streamingStaticModelClient{
			deltas: []ModelStreamEvent{{Kind: ModelStreamEventToolCallBoundary}},
			response: ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID: "provider-raw", Name: "noop", Arguments: json.RawMessage(`{}`),
			}}}},
		},
		OnModelDelta: func(event ModelStreamEvent) error {
			if event.Kind == ModelStreamEventToolCallBoundary {
				boundaryObserved = true
				if publishedID != "" {
					t.Fatal("tool proposal was published before its complete response")
				}
			}
			return nil
		},
		OnEventError: func(event Event) error {
			switch event.Type {
			case EventModelResponse:
				if !boundaryObserved || len(event.ToolCalls) != 1 || event.ToolCalls[0].ID == "provider-raw" {
					t.Fatalf("invalid canonical publication: %#v", event)
				}
				publishedID = event.ToolCalls[0].ID
			case EventToolStarted, EventToolCompleted:
				if publishedID == "" || event.ToolCallID != publishedID {
					t.Fatalf("tool lifecycle lost its published identity: %#v", event)
				}
				if event.Type == EventToolCompleted {
					completedID = event.ToolCallID
				}
			}
			return nil
		},
		Tools: FuncToolGateway(func(_ context.Context, call ToolCall) (ToolResult, error) {
			if call.ID != publishedID || publishedID == "" {
				t.Fatal("tool executed before canonical publication")
			}
			executions++
			return ToolResult{Value: map[string]any{"ok": true}, Terminal: true}, nil
		}),
	}
	_, err := engine.Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Run."}}, Tools: []ToolSchema{{Name: "noop"}}})
	if err != nil || executions != 1 || completedID != publishedID || publishedID == "" {
		t.Fatalf("stream lifecycle: executions=%d published=%q completed=%q err=%v", executions, publishedID, completedID, err)
	}
}

func TestDurableToolCallIdentityRejectsMalformedProviderBatchBeforeExecution(t *testing.T) {
	for _, calls := range [][]ToolCall{
		{{ID: "call_0", Name: "noop"}, {ID: "call_0", Name: "noop"}},
		{{ID: strings.Repeat("x", 513), Name: "noop"}},
		{{ID: " call_0", Name: "noop"}},
	} {
		client := &capturingRequestModelClient{responses: []ModelResponse{{Message: Message{Role: "assistant", ToolCalls: calls}}}}
		published, executed := 0, 0
		engine := Engine{Model: client, ToolCallIdentityPolicy: DurableToolCallIdentity,
			Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
				executed++
				return ToolResult{}, nil
			}),
			OnEvent: func(event Event) {
				if event.Type == EventModelResponse {
					published++
				}
			},
		}
		_, err := engine.Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Run."}}})
		var invalid *ToolCallBatchValidationError
		if !errors.As(err, &invalid) || executed != 0 || published != 0 {
			t.Fatalf("invalid batch was canonicalized into validity: err=%v published=%d executed=%d", err, published, executed)
		}
	}
	if _, err := newToolCallIdentityScope("unknown"); err == nil {
		t.Fatal("unknown identity policy must fail closed")
	}
}
