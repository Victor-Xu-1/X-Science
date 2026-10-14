package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type batchSourceGateway struct {
	results map[string]ToolResult
}

func (gateway batchSourceGateway) ToolSchemas() []ToolSchema { return []ToolSchema{{Name: "repl"}} }
func (gateway batchSourceGateway) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	return gateway.results[call.ID], nil
}

func TestSourceObservationBatchPreservesVerifiedRepetition(t *testing.T) {
	for _, test := range []struct {
		name        string
		observation *SourceObservation
		material    bool
		wantState   SourceObservationState
		wantStop    bool
	}{
		{"unobserved tail", nil, false, SourceObservationRepeated, true},
		{"unknown tail", &SourceObservation{State: SourceObservationUnknown}, false, SourceObservationRepeated, true},
		{"incomplete source tail", &SourceObservation{State: SourceObservationUnknown, HasSources: true}, false, SourceObservationRepeated, true},
		{"new source tail", &SourceObservation{State: SourceObservationNew, HasSources: true}, false, SourceObservationNew, false},
		{"material computation tail", nil, true, SourceObservationRepeated, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tail := map[string]any{"ok": true, "reused": true}
			if test.material {
				tail = map[string]any{"ok": true, "effect": map[string]any{"schema": ToolEffectSchema, "state": string(ToolEffectChanged), "categories": []any{"computation"}}}
			}
			gateway := batchSourceGateway{results: map[string]ToolResult{
				"source": {Value: map[string]any{"ok": true, "stdout": "same source"}, SourceObservation: &SourceObservation{State: SourceObservationRepeated, HasSources: true, RepeatedOperations: 3}},
				"tail":   {Value: tail, SourceObservation: test.observation},
			}}
			calls := []ToolCall{{ID: "source", Name: "repl", Arguments: json.RawMessage(`{}`)}, {ID: "tail", Name: "repl", Arguments: json.RawMessage(`{}`)}}
			batch, err := (Engine{Tools: gateway}).ExecuteToolBatch(context.Background(), calls, 0, MediaPolicy{}, 0)
			if err != nil || batch.NextOrdinal != 2 || len(batch.Messages) != 2 {
				t.Fatalf("calls did not execute once in order: %+v %v", batch, err)
			}
			if batch.SourceObservation == nil || batch.SourceObservation.State != test.wantState {
				t.Fatalf("batch lost strongest completed observation: %+v", batch.SourceObservation)
			}
			_, err = sourceObservationRecoveryFeedback(nil, batch.SourceObservation, batch.Messages, 3, nil)
			var stopped *ToolRoundNoProgressError
			if errors.As(err, &stopped) != test.wantStop || stopped != nil && len(stopped.Calls) != 0 {
				t.Fatalf("wrong feedback or unknown execution closed: %v", err)
			}
		})
	}
}

type batchedSourceModel struct{ requests int }

func (model *batchedSourceModel) Complete(_ context.Context, _ ModelRequest) (ModelResponse, error) {
	model.requests++
	args, _ := json.Marshal(map[string]any{"code": fmt.Sprint("actual_work_", model.requests)})
	return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{
		{ID: fmt.Sprint("source-", model.requests), Name: "repl", Arguments: args},
		{ID: fmt.Sprint("tail-", model.requests), Name: "repl", Arguments: json.RawMessage(`{"code":"inspect_existing_receipt()"}`)},
	}}}, nil
}

type batchedSourceWindowGateway struct {
	window SourceObservationWindow
	calls  int
}

func (gateway *batchedSourceWindowGateway) ToolSchemas() []ToolSchema {
	return []ToolSchema{{Name: "repl"}}
}
func (gateway *batchedSourceWindowGateway) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	gateway.calls++
	if strings.HasPrefix(call.ID, "tail-") {
		return ToolResult{Value: map[string]any{"ok": true, "reused": true}}, nil
	}
	gateway.window.Observe(call.ID, strings.Repeat("a", 64))
	observation := gateway.window.Complete(call.ID, false)
	return ToolResult{Value: map[string]any{"ok": true, "stdout": "same verified source"}, SourceObservation: &observation}, nil
}

func TestSourceObservationBatchedEngineCannotEraseBoundedFeedback(t *testing.T) {
	model, gateway := &batchedSourceModel{}, &batchedSourceWindowGateway{}
	_, err := (Engine{Model: model, Tools: gateway}).Run(context.Background(), RunRequest{
		Messages: []Message{{Role: "user", Content: "Continue research"}}, Tools: gateway.ToolSchemas(),
		MaxToolRounds: 8, MaxConsecutiveIdenticalToolRounds: 3,
	})
	var stopped *ToolRoundNoProgressError
	if !errors.As(err, &stopped) || !stopped.SourceRepetition || len(stopped.Calls) != 0 || model.requests != 4 || gateway.calls != 8 {
		t.Fatalf("trailing no-op erased bounded source feedback or skipped execution: requests=%d calls=%d error=%v", model.requests, gateway.calls, err)
	}
}

func TestSourceObservationBatchKeepsAnyNewSource(t *testing.T) {
	for _, middle := range []*SourceObservation{nil, {State: SourceObservationUnknown}, {State: SourceObservationRepeated, HasSources: true, RepeatedOperations: 1}} {
		gateway := batchSourceGateway{results: map[string]ToolResult{
			"new":    {Value: map[string]any{"ok": true}, SourceObservation: &SourceObservation{State: SourceObservationNew, HasSources: true}},
			"middle": {Value: map[string]any{"ok": true, "reused": true}, SourceObservation: middle},
			"last":   {Value: map[string]any{"ok": true}, SourceObservation: &SourceObservation{State: SourceObservationRepeated, HasSources: true, RepeatedOperations: 3}},
		}}
		calls := []ToolCall{}
		for _, id := range []string{"new", "middle", "last"} {
			calls = append(calls, ToolCall{ID: id, Name: "repl", Arguments: json.RawMessage(`{}`)})
		}
		batch, err := (Engine{Tools: gateway}).ExecuteToolBatch(context.Background(), calls, 0, MediaPolicy{}, 0)
		if err != nil || batch.SourceObservation == nil || batch.SourceObservation.State != SourceObservationNew {
			t.Fatalf("later source/no-op erased actual new evidence: middle=%+v batch=%+v err=%v", middle, batch.SourceObservation, err)
		}
		if _, err := sourceObservationRecoveryFeedback(nil, batch.SourceObservation, batch.Messages, 3, nil); err != nil {
			t.Fatalf("batch with new evidence was interrupted: %v", err)
		}
	}
}

type newHeadSourceModel struct{ requests int }

func (model *newHeadSourceModel) Complete(_ context.Context, _ ModelRequest) (ModelResponse, error) {
	model.requests++
	calls := []ToolCall{}
	for index := 0; index < 4; index++ {
		args, _ := json.Marshal(map[string]any{"code": fmt.Sprint("actual_work_", model.requests, "_", index)})
		calls = append(calls, ToolCall{ID: fmt.Sprint("source-", model.requests, "-", index), Name: "repl", Arguments: args})
	}
	return ModelResponse{Message: Message{Role: "assistant", ToolCalls: calls}}, nil
}

func TestSourceObservationBatchedEngineNewHeadDefersRepetitionBudget(t *testing.T) {
	model, gateway := &newHeadSourceModel{}, &batchedSourceWindowGateway{}
	_, err := (Engine{Model: model, Tools: gateway}).Run(context.Background(), RunRequest{
		Messages: []Message{{Role: "user", Content: "Continue research"}}, Tools: gateway.ToolSchemas(),
		MaxToolRounds: 4, MaxConsecutiveIdenticalToolRounds: 3,
	})
	var stopped *ToolRoundNoProgressError
	if !errors.As(err, &stopped) || !stopped.SourceRepetition || len(stopped.Calls) != 0 || model.requests != 2 || gateway.calls != 8 {
		t.Fatalf("new evidence was lost or repeated-only next batch escaped: requests=%d calls=%d error=%v", model.requests, gateway.calls, err)
	}
}
