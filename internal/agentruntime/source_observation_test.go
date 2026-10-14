package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSourceObservationRecoveryFeedbackPreservesErrorPrecedence(t *testing.T) {
	prior := errors.New("original tool failure")
	repeated := &SourceObservation{State: SourceObservationRepeated, RepeatedOperations: 3}
	for _, test := range []struct {
		name        string
		observation *SourceObservation
		limit       int
		prior       error
		feedback    bool
		exhausted   bool
	}{
		{"unobserved", nil, 3, nil, false, false},
		{"new", &SourceObservation{State: SourceObservationNew}, 3, nil, false, false},
		{"below limit", repeated, 4, nil, true, false},
		{"disabled limit", repeated, 0, nil, true, false},
		{"exhausted", repeated, 3, nil, true, true},
		{"original failure", repeated, 3, prior, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages, err := sourceObservationRecoveryFeedback(nil, test.observation, nil, test.limit, test.prior)
			if (len(messages) == 1) != test.feedback {
				t.Fatalf("feedback=%#v", messages)
			}
			var exhausted *ToolRoundNoProgressError
			if errors.As(err, &exhausted) != test.exhausted || test.prior != nil && err != test.prior || !test.exhausted && test.prior == nil && err != nil {
				t.Fatalf("wrong error precedence: %v", err)
			}
			if exhausted != nil && (!exhausted.SourceRepetition || len(exhausted.Calls) != 0 || exhausted.Limit != test.limit) {
				t.Fatalf("source observation closed unknown calls: %#v", exhausted)
			}
		})
	}
}

func TestSourceObservationIdentityAndWindow(t *testing.T) {
	identity := SourceEvidenceIdentity{Class: "bundled-readonly-source-v1", ConnectorID: "bundled:fixture", ConnectorSource: "bundled", ToolName: "mcp__fixture__search", InputSchemaSHA256: strings.Repeat("a", 64), RequestSHA256: strings.Repeat("b", 64), ResultSHA256: strings.Repeat("c", 64)}
	key, ok := identity.Key()
	if !ok {
		t.Fatal("valid source identity rejected")
	}
	tracker := SourceObservationWindow{}
	for i, want := range []SourceObservationState{SourceObservationNew, SourceObservationRepeated, SourceObservationRepeated} {
		call := fmt.Sprint(i)
		tracker.Observe(call, key)
		observation := tracker.Complete(call, false)
		if observation.State != want || observation.RepeatedOperations != i {
			t.Fatalf("operation %d: %+v", i, observation)
		}
	}
	tracker.Observe("changed", key)
	if got := tracker.Complete("changed", true); got.RepeatedOperations != 0 {
		t.Fatalf("material change did not reset: %+v", got)
	}
	changed := identity
	changed.RequestSHA256 = strings.Repeat("d", 64)
	otherKey, valid := changed.Key()
	if !valid || key == otherKey {
		t.Fatal("changed actual parameters collapsed")
	}
	tracker.Observe("next-page", otherKey)
	if got := tracker.Complete("next-page", false); got.State != SourceObservationNew {
		t.Fatalf("new source: %+v", got)
	}
	identity.ResultSHA256 = "invalid"
	if _, valid := identity.Key(); valid {
		t.Fatal("invalid digest accepted")
	}
	if got := tracker.Complete("unknown", false); got.State != SourceObservationUnknown {
		t.Fatalf("unknown execution invented evidence: %+v", got)
	}
}

func TestSourceObservationOverflowAndFreshScope(t *testing.T) {
	window := SourceObservationWindow{}
	for i := 0; i <= maxSourceObservationKeys; i++ {
		window.Observe("large", fmt.Sprintf("%064x", i+1))
	}
	if got := window.Complete("large", false); got.State != SourceObservationUnknown {
		t.Fatalf("overflow silently became complete: %+v", got)
	}
	window = SourceObservationWindow{}
	window.Observe("fresh", strings.Repeat("a", 64))
	if got := window.Complete("fresh", false); got.State != SourceObservationNew {
		t.Fatalf("new scope inherited old evidence: %+v", got)
	}
}

type sourceWrapperModel struct {
	requests int
	feedback []string
}

func (m *sourceWrapperModel) Complete(_ context.Context, request ModelRequest) (ModelResponse, error) {
	m.requests++
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "verified source") {
			m.feedback = append(m.feedback, message.Content)
		}
	}
	args, _ := json.Marshal(map[string]any{"code": fmt.Sprintf("print('label-%d'); unknown_work()", m.requests)})
	return ModelResponse{Message: Message{Role: "assistant", Content: "Working on the next action.", ToolCalls: []ToolCall{{ID: fmt.Sprint(m.requests), Name: "repl", Arguments: args}}}}, nil
}

type sourceWrapperGateway struct {
	window           SourceObservationWindow
	calls            int
	materialAt       int
	deferObservation bool
	pending          *SourceObservation
	pendingCallID    string
}

func (g *sourceWrapperGateway) ToolSchemas() []ToolSchema { return []ToolSchema{{Name: "repl"}} }
func (g *sourceWrapperGateway) Execute(_ context.Context, call ToolCall) (ToolResult, error) {
	g.calls++
	g.window.Observe(call.ID, strings.Repeat("a", 64))
	observation := SourceObservation{State: SourceObservationUnknown}
	if !g.deferObservation {
		observation = g.window.Complete(call.ID, g.calls == g.materialAt)
	}
	g.pending, g.pendingCallID = &observation, call.ID
	return ToolResult{Value: map[string]any{"ok": true, "stdout": fmt.Sprint("actual result ", g.calls), "files_written": []any{}}, SourceObservation: &observation}, nil
}

func TestSourceObservationEngineConsumesPostCommitHostUpdate(t *testing.T) {
	model, gateway := &sourceWrapperModel{}, &sourceWrapperGateway{deferObservation: true}
	engine := Engine{Model: model, Tools: gateway, OnEvent: func(event Event) {
		if event.Type == EventToolCompleted {
			*gateway.pending = gateway.window.Complete(gateway.pendingCallID, false)
		}
	}}
	_, err := engine.Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Continue research"}}, Tools: gateway.ToolSchemas(), MaxConsecutiveIdenticalToolRounds: 3})
	var stopped *ToolRoundNoProgressError
	if !errors.As(err, &stopped) || !stopped.SourceRepetition || gateway.calls != 4 || len(stopped.Calls) != 0 {
		t.Fatalf("post-commit observation not consumed exactly once: calls=%d error=%v", gateway.calls, err)
	}
	encoded, _ := json.Marshal(ToolResult{Value: map[string]any{"ok": true}, SourceObservation: gateway.pending})
	if strings.Contains(string(encoded), "SourceObservation") || strings.Contains(string(encoded), "RepeatedOperations") {
		t.Fatal("host-only metadata entered serialized tool value")
	}
}

func TestSourceObservationEngineBoundsWithoutClosingUnknownExecution(t *testing.T) {
	model, gateway := &sourceWrapperModel{}, &sourceWrapperGateway{}
	result, err := (Engine{Model: model, Tools: gateway}).Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Continue research"}}, Tools: gateway.ToolSchemas(), MaxConsecutiveIdenticalToolRounds: 3})
	var stopped *ToolRoundNoProgressError
	if !errors.As(err, &stopped) || !stopped.SourceRepetition || len(stopped.Calls) != 0 {
		t.Fatalf("wrong correction/closed actions: %v %+v", err, stopped)
	}
	if gateway.calls != 4 || model.requests != 4 {
		t.Fatalf("execution was skipped or unbounded: calls=%d requests=%d", gateway.calls, model.requests)
	}
	if len(model.feedback) == 0 {
		t.Fatal("model received no source feedback before bounded correction")
	}
	for _, feedback := range model.feedback {
		if strings.Contains(feedback, "idempotent") || strings.Contains(feedback, "operations are closed") {
			t.Fatalf("source feedback claimed execution equivalence: %s", feedback)
		}
	}
	last := result.Messages[len(result.Messages)-1]
	if !strings.Contains(last.Content, "actual result 4") {
		t.Fatalf("last executed result lost: %+v", last)
	}
}

func TestSourceObservationMaterialComputationReopensWindow(t *testing.T) {
	model, gateway := &sourceWrapperModel{}, &sourceWrapperGateway{materialAt: 3}
	_, err := (Engine{Model: model, Tools: gateway}).Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Continue research"}}, Tools: gateway.ToolSchemas(), MaxConsecutiveIdenticalToolRounds: 3})
	var stopped *ToolRoundNoProgressError
	if !errors.As(err, &stopped) || !stopped.SourceRepetition || gateway.calls != 6 {
		t.Fatalf("material computation ignored: calls=%d err=%v", gateway.calls, err)
	}
}
