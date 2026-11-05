package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type textToolClosedModel struct {
	postAction bool
	requests   []ModelRequest
	content    string
}

func (model *textToolClosedModel) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	return model.CompleteStream(ctx, request, nil)
}

func (model *textToolClosedModel) CompleteStream(_ context.Context, request ModelRequest, emit func(ModelStreamEvent) error) (ModelResponse, error) {
	model.requests = append(model.requests, request)
	if model.postAction && len(model.requests) == 1 {
		return ModelResponse{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "before-closed", Name: "edit_file", Arguments: json.RawMessage(`{}`),
		}}}}, nil
	}
	if emit != nil {
		for _, character := range model.content {
			if err := emit(ModelStreamEvent{Kind: ModelStreamEventContentDelta, ContentDelta: string(character)}); err != nil {
				return ModelResponse{}, err
			}
		}
	}
	return ModelResponse{Message: Message{Role: "assistant", Content: model.content}}, nil
}

type textToolClosedNonStreaming struct{ model *textToolClosedModel }

func (client textToolClosedNonStreaming) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	return client.model.Complete(ctx, request)
}

type textToolClosedGateway struct{ calls int }

func (gateway *textToolClosedGateway) Execute(context.Context, ToolCall) (ToolResult, error) {
	gateway.calls++
	return ToolResult{Value: map[string]any{"ok": true}}, nil
}

func (*textToolClosedGateway) RequiredToolChoice([]Message, []ToolSchema) any { return "none" }

func TestEngineRejectsTextToolEnvelopeWhenExecutionClosed(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, postAction := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/postAction=%t", streaming, postAction), func(t *testing.T) {
				model := &textToolClosedModel{postAction: postAction, content: "<tool_call><function=edit_file><parameter=content>SYNTHETIC</parameter></function></tool_call>"}
				var client ModelClient = model
				if !streaming {
					client = textToolClosedNonStreaming{model}
				}
				gateway := &textToolClosedGateway{}
				var published strings.Builder
				engine := Engine{Model: client, Tools: gateway, OnModelDelta: func(event ModelStreamEvent) error {
					published.WriteString(event.ContentDelta)
					return nil
				}}
				request := RunRequest{Messages: []Message{{Role: "user", Content: "Finish the current authorized work."}}, Tools: []ToolSchema{{Name: "edit_file"}}, MaxToolRounds: 2}
				wantCalls := 0
				if postAction {
					wantCalls = 1
				} else {
					request.InitialToolChoice = "none"
				}
				result, err := engine.Run(t.Context(), request)
				last := model.requests[len(model.requests)-1]
				if len(last.Tools) != 0 || last.ToolChoice != "none" {
					t.Fatal("fixture did not reach the actual closed provider request")
				}
				var invalid *TextToolEnvelopePresentationError
				if !errors.As(err, &invalid) || result.FinalMessage.Content != "" || published.Len() != 0 || gateway.calls != wantCalls {
					t.Fatalf("closed round accepted or exposed text action: err=%v final_bytes=%d published_bytes=%d calls=%d", err, len(result.FinalMessage.Content), published.Len(), gateway.calls)
				}
				if postAction {
					kept := false
					for _, message := range result.Messages {
						kept = kept || message.Role == "tool" && message.ToolCallID == "before-closed"
					}
					if !kept {
						t.Fatal("closed presentation failure discarded the prior native receipt")
					}
				}
			})
		}
	}
}

func TestEngineClosedRoundPreservesExamplesAndNoToolContext(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		known   bool
	}{
		{"fenced", "```xml\n<tool_call><function=edit_file>example</function></tool_call>\n```", true},
		{"quoted", "\"<tool_call><function=edit_file>example</function></tool_call>\"", true},
		{"no_tool_context", "<tool_call><function=edit_file>ordinary text request</function></tool_call>", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &textToolClosedModel{content: test.content}
			gateway := &textToolClosedGateway{}
			request := RunRequest{Messages: []Message{{Role: "user", Content: "Describe this example."}}, InitialToolChoice: "none"}
			if test.known {
				request.Tools = []ToolSchema{{Name: "edit_file"}}
			}
			result, err := (Engine{Model: model, Tools: gateway}).Run(t.Context(), request)
			if err != nil || result.FinalMessage.Content != test.content || gateway.calls != 0 ||
				len(model.requests[0].Tools) != 0 || model.requests[0].ToolChoice != "none" {
				t.Fatalf("closed ordinary content or provider policy changed: error=%v calls=%d", err, gateway.calls)
			}
		})
	}
}
