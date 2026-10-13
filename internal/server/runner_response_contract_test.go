package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestResponseContractProjectsProgressWithoutChangingToolAuthority(t *testing.T) {
	original := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "inspect", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false,
	}}}, ToolChoice: map[string]any{"type": "tool", "name": "inspect"}}
	projected, err := projectRunnerResponseContract(original, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := original.Tools[0].Parameters["properties"].(map[string]any)[runnerPublicProgressField]; found {
		t.Fatal("mutated executable tool schema")
	}
	if !reflect.DeepEqual(projected.Tools, original.Tools) || !reflect.DeepEqual(projected.ToolChoice, original.ToolChoice) || len(original.Messages) != 0 {
		t.Fatal("progress guidance changed executable authority")
	}
	if len(projected.Messages) != 1 || projected.Messages[0].Content != runnerPublicProgressCadence(true) {
		t.Fatal("missing request-scoped public progress cadence")
	}
}

func TestResponseContractSharesInformativeBoundedProgressStyle(t *testing.T) {
	request, err := projectRunnerResponseContract(agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "inspect"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if request.Messages[0].Content != runnerPublicProgressCadence(false) ||
		!strings.Contains(sessionRunnerPublicCommunicationContract, sessionRunnerPublicProgressStyle) ||
		!strings.Contains(sessionRunnerPublicCommunicationContract, agentruntime.PublicProgressEnvelopeContract) ||
		request.Tools[0].Parameters != nil {
		t.Fatal("progress style diverged or became a mandatory verbosity gate")
	}
	text := "已读取版本记录，内容与要求一致；还需核对当前环境实际导入的版本，排除文件记录与运行环境不一致的情况。下一步将比较两者，并保留读取结果。"
	if got := sessionRunnerPublicProgressNarration(text); got != text {
		t.Fatalf("informative bounded progress was lost: %q", got)
	}
}

func TestResponseContractLeavesToolFreePresentationRequestUnchanged(t *testing.T) {
	original := agentruntime.ModelRequest{Messages: []agentruntime.Message{
		{Role: "system", Content: "Translate only the supplied text."},
		{Role: "user", Content: "source text"},
	}, ReasoningMode: agentruntime.ReasoningModeDisabled}
	for _, due := range []bool{false, true} {
		projected, err := projectRunnerResponseContract(original, due)
		if err != nil || !reflect.DeepEqual(projected, original) {
			t.Fatalf("presentation-only request acquired action guidance: %+v error=%v", projected, err)
		}
	}
}

func TestResponseContractParallelInstructionsMatchProgressCadence(t *testing.T) {
	for _, test := range []struct {
		name        string
		due         bool
		instruction string
		forbidden   string
	}{
		{"due", true, "Begin with one complete public progress envelope", "Every call"},
		{"not-due", false, "An additional public progress update is optional", "must include"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{
				{Name: "inspect_a", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false}},
				{Name: "inspect_b", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false}},
			}}
			projected, err := projectRunnerResponseContract(original, test.due)
			if err != nil {
				t.Fatal(err)
			}
			if len(projected.Messages) != 1 || !strings.Contains(projected.Messages[0].Content, test.instruction) || strings.Contains(projected.Messages[0].Content, test.forbidden) {
				t.Fatalf("contradictory request progress cadence: %+v", projected.Messages)
			}
			for index, tool := range projected.Tools {
				if !reflect.DeepEqual(tool, original.Tools[index]) {
					t.Fatalf("tool %s acquired a competing progress schema", tool.Name)
				}
				if _, exists := original.Tools[index].Parameters["properties"].(map[string]any)[runnerPublicProgressField]; exists {
					t.Fatal("model-facing progress mutated executable authority")
				}
			}
		})
	}
}

func TestResponseContractStreamsOnePrimaryProgressAndStripsControlArgument(t *testing.T) {
	text := "先核对已有数据的范围，再比较可用记录。"
	model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "inspect-1", Name: "inspect", Arguments: json.RawMessage(`{"path":"data.csv","public_progress":"` + text + `"}`)}}}}}}
	client := &sessionRunnerResponseContractClient{delegate: model, progressDue: func() bool { return true }}
	var events []agentruntime.ModelStreamEvent
	response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "inspect", Parameters: map[string]any{"type": "object"}}}}, func(event agentruntime.ModelStreamEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || response.Message.Content != text || strings.Contains(string(response.Message.ToolCalls[0].Arguments), runnerPublicProgressField) {
		t.Fatalf("calls=%d response=%+v", model.calls, response.Message)
	}
	if len(events) != 3 || events[0].Kind != agentruntime.ModelStreamEventToolCallBoundary || events[1].Kind != agentruntime.ModelStreamEventPublicProgressDelta || events[1].ContentDelta != text || events[2].Kind != agentruntime.ModelStreamEventPublicProgressBoundary {
		t.Fatalf("events=%+v", events)
	}
}

func TestResponseContractSelectsStructuredProgressOnce(t *testing.T) {
	model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Content: "Checking the records.", ToolCalls: []agentruntime.ToolCall{{ID: "check", Name: "inspect", Arguments: json.RawMessage(`{"public_progress":"I will check the records."}`)}}}}}}
	client := &sessionRunnerResponseContractClient{delegate: model}
	visible := ""
	response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
			visible += event.ContentDelta
		}
		return nil
	})
	if err != nil || visible != "I will check the records." || response.Message.Content != visible {
		t.Fatalf("visible=%q response=%q err=%v", visible, response.Message.Content, err)
	}
}

func TestResponseContractDoesNotDiscardNewProgressBetweenCadenceRequests(t *testing.T) {
	text := "The first source is unavailable; I will check the remaining source."
	args, _ := json.Marshal(map[string]any{"public_progress": text})
	model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{
		Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{
			ID: "new-finding", Name: "inspect", Arguments: args,
		}}},
	}}}
	client := &sessionRunnerResponseContractClient{delegate: model, progressDue: func() bool { return false }}
	var visible string
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		visible += event.ContentDelta
		return nil
	})
	if err != nil || visible != text || model.calls != 1 {
		t.Fatalf("new progress silently discarded: %q calls=%d error=%v", visible, model.calls, err)
	}
}

type interruptedProgressModel struct{}

func (interruptedProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return agentruntime.ModelResponse{}, context.DeadlineExceeded
}
func (interruptedProgressModel) CompleteStream(_ context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if emit != nil {
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "unfinished candidate"}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
	}
	return agentruntime.ModelResponse{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{ID: "interrupted", Name: "inspect", Arguments: json.RawMessage(`{"public_progress":"A claimed update."}`)}}}}, context.DeadlineExceeded
}

func TestResponseContractDoesNotPublishStructuredProgressFromInterruptedProvider(t *testing.T) {
	client := &sessionRunnerResponseContractClient{delegate: interruptedProgressModel{}}
	var events []agentruntime.ModelStreamEvent
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error { events = append(events, event); return nil })
	if err != context.DeadlineExceeded || len(events) != 1 || events[0].Kind != agentruntime.ModelStreamEventContentDelta {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestResponseContractPropagatesPublicationCancellation(t *testing.T) {
	model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{ID: "a", Name: "inspect", Arguments: json.RawMessage(`{"public_progress":"Checking the record."}`)}}}}}}
	client := &sessionRunnerResponseContractClient{delegate: model}
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
			return context.Canceled
		}
		return nil
	})
	if err != context.Canceled || model.calls != 1 {
		t.Fatalf("calls=%d err=%v", model.calls, err)
	}
}

func TestResponseContractPreservesInvalidFieldToolArgumentsAndRejectsSchemaCollision(t *testing.T) {
	response := agentruntime.ModelResponse{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{ID: "call", Name: "inspect", Arguments: json.RawMessage(`{"path":"data.csv","public_progress":{"text":"not the contract"}}`)}}}}
	progress, _ := extractRunnerResponseProgress(&response)
	if progress != "" || string(response.Message.ToolCalls[0].Arguments) != `{"path":"data.csv"}` {
		t.Fatalf("response=%+v", response)
	}
	_, err := projectRunnerResponseContract(agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "collision", Parameters: map[string]any{"properties": map[string]any{runnerPublicProgressField: map[string]any{"type": "number"}}}}}}, true)
	if err == nil {
		t.Fatal("overwrote an executable argument")
	}
}

func TestResponseContractConsolidatesBatchRegardlessOfRequestCadence(t *testing.T) {
	for _, due := range []bool{false, true} {
		secondProgress := ""
		if due {
			secondProgress = "Comparing the two records."
		}
		secondArgs, err := json.Marshal(map[string]any{"path": "b.csv", runnerPublicProgressField: secondProgress})
		if err != nil {
			t.Fatal(err)
		}
		model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{
			{ID: "a", Name: "inspect_a", Arguments: json.RawMessage(`{"path":"a.csv","public_progress":"Comparing the two records."}`)},
			{ID: "b", Name: "inspect_b", Arguments: secondArgs},
		}}}}}
		client := &sessionRunnerResponseContractClient{delegate: model, progressDue: func() bool { return due }}
		count := 0
		response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
			if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
				count++
			}
			return nil
		})
		if err != nil || count != 1 || model.calls != 1 || len(response.Message.ToolCalls) != 2 {
			t.Fatalf("due=%t count=%d err=%v", due, count, err)
		}
		for index, call := range response.Message.ToolCalls {
			want := []string{`{"path":"a.csv"}`, `{"path":"b.csv"}`}[index]
			if string(call.Arguments) != want {
				t.Fatalf("control field reached execution or executable input changed: %s", call.Arguments)
			}
		}
	}
}

func TestResponseContractDoesNotPromoteInterruptedOrFinalDrafts(t *testing.T) {
	for _, text := range []string{"", "报告已生成。", strings.Repeat("x", 301)} {
		args, _ := json.Marshal(map[string]any{"path": "data.csv", runnerPublicProgressField: text})
		model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "call", Name: "inspect", Arguments: args}}}}}}
		client := &sessionRunnerResponseContractClient{delegate: model, progressDue: func() bool { return true }}
		var visible string
		response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error { visible += event.ContentDelta; return nil })
		if err != nil || visible != "" || strings.Contains(string(response.Message.ToolCalls[0].Arguments), runnerPublicProgressField) {
			t.Fatalf("text=%q visible=%q err=%v", text, visible, err)
		}
	}
}

func TestResponseContractProtocolDiagnosticCannotPromoteNativeOrStructuredDraft(t *testing.T) {
	for _, args := range []string{`{}`, `{"public_progress":"Checking the source."}`} {
		model := &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{
			Content: "Checking the source.", ToolCalls: []agentruntime.ToolCall{{
				ID: "invalid-arguments", Name: "inspect", Arguments: json.RawMessage(args),
				ProviderProtocolDiagnostic: "invalid JSON object arguments",
			}},
		}}}}
		client := &sessionRunnerResponseContractClient{delegate: model}
		public := 0
		response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
			if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
				public++
			}
			return nil
		})
		if err != nil || public != 0 || response.Message.ToolCalls[0].ProviderProtocolDiagnostic == "" {
			t.Fatalf("malformed proposal was promoted or lost its diagnostic: public=%d err=%v", public, err)
		}
	}
}
