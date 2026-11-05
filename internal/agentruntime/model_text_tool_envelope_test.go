package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestModelRoundRejectsBareTextToolEnvelopeBeforePublication(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/truncated=%t", streaming, truncated), func(t *testing.T) {
				raw := "<tool_call><function=edit_file><parameter=content>SYNTHETIC_ARGUMENT"
				if !truncated {
					raw += "</parameter></function></tool_call>"
				}
				base := publicProgressFacadeStatic{response: ModelResponse{Message: Message{Role: "assistant", Content: raw}}}
				var model ModelClient = &base
				if streaming {
					events := make([]ModelStreamEvent, 0, len(raw))
					for _, character := range raw {
						events = append(events, ModelStreamEvent{Kind: ModelStreamEventContentDelta, ContentDelta: string(character)})
					}
					model = &publicProgressFacadeStreaming{publicProgressFacadeStatic: base, events: events}
				}
				var published strings.Builder
				engine := Engine{Model: model, OnModelDelta: func(event ModelStreamEvent) error {
					published.WriteString(event.ContentDelta)
					return nil
				}}
				response, _, err := engine.completeModelRound(context.Background(), ModelRequest{
					Tools: []ToolSchema{{Name: "edit_file"}},
				}, false)
				if err == nil || response.Message.Content != "" || published.Len() != 0 {
					t.Fatalf("bare text tool request accepted or exposed: err=%v final_bytes=%d published_bytes=%d", err, len(response.Message.Content), published.Len())
				}
			})
		}
	}
}

func TestModelTextToolProbePreservesOrdinaryContentAndFirstDelta(t *testing.T) {
	for _, content := range []string{
		"Ordinary response.", "<html><body>Report</body></html>", "<note><function=edit_file>example</function></note>",
		"```xml\n<tool_call><function=edit_file><parameter=x>example</parameter></function></tool_call>\n```",
		"\"<tool_call><function=edit_file>quoted example\"", "> <tool_call><function=edit_file>quoted example",
		"<tool_call><function=unknown_xml>ordinary XML</function></tool_call>",
	} {
		var text strings.Builder
		probe := newModelTextToolEnvelopeProbe([]ToolSchema{{Name: "edit_file"}}, func(value string) error { text.WriteString(value); return nil })
		for _, character := range content {
			if err := probe.write(string(character)); err != nil {
				t.Fatalf("ordinary text rejected: %v", err)
			}
		}
		if err := probe.finish(); err != nil || text.String() != content {
			t.Fatalf("ordinary text changed: got_bytes=%d want_bytes=%d err=%v", text.Len(), len(content), err)
		}
	}
	var text strings.Builder
	probe := newModelTextToolEnvelopeProbe([]ToolSchema{{Name: "edit_file"}}, func(value string) error { text.WriteString(value); return nil })
	if err := probe.write("O"); err != nil || text.String() != "O" {
		t.Fatal("ordinary first character waited for the final response")
	}
	raw := "<tool_call><function=edit_file>ordinary non-tool request</function></tool_call>"
	text.Reset()
	probe = newModelTextToolEnvelopeProbe(nil, func(value string) error { text.WriteString(value); return nil })
	if err := probe.write(raw); err != nil || text.String() != raw {
		t.Fatal("a request without advertised tools acquired a tool presentation policy")
	}
}

func TestModelTextToolEnvelopeChecksTerminalOnlyAndPreservesErrors(t *testing.T) {
	raw := "<tool_call><function=edit_file><parameter=content>SYNTHETIC</parameter></function></tool_call>"
	for _, observed := range []string{"", "Ordinary observed content."} {
		model := &publicProgressFacadeStreaming{publicProgressFacadeStatic: publicProgressFacadeStatic{response: ModelResponse{Message: Message{Content: raw}}}}
		if observed != "" {
			model.events = []ModelStreamEvent{{Kind: ModelStreamEventContentDelta, ContentDelta: observed}}
		}
		_, _, err := (Engine{Model: model}).completeModelRound(context.Background(), ModelRequest{Tools: []ToolSchema{{Name: "edit_file"}}}, false)
		var invalid *TextToolEnvelopePresentationError
		if !errors.As(err, &invalid) {
			t.Fatalf("terminal-only tool envelope accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := (Engine{Model: &publicProgressFacadeStatic{response: ModelResponse{Message: Message{Content: raw}}}}).completeModelRound(ctx, ModelRequest{Tools: []ToolSchema{{Name: "edit_file"}}}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	cause := &TextToolEnvelopePresentationError{}
	_, _, err = (Engine{Model: &publicProgressFacadeStatic{response: ModelResponse{Message: Message{Content: "ordinary"}}},
		OnModelDelta: func(ModelStreamEvent) error { return cause }}).completeModelRound(context.Background(), ModelRequest{Tools: []ToolSchema{{Name: "edit_file"}}}, false)
	if !errors.Is(err, cause) {
		t.Fatalf("publication failure swallowed: %v", err)
	}
}

func TestModelTextToolProbeBoundsAndTruncatedFunction(t *testing.T) {
	for _, content := range []string{"<tool_call><function=", "<tool_call><function=edit_file", "<tool_call><function=edi"} {
		var text strings.Builder
		probe := newModelTextToolEnvelopeProbe([]ToolSchema{{Name: "edit_file"}}, func(value string) error { text.WriteString(value); return nil })
		if err := probe.write(content); err != nil {
			t.Fatal(err)
		}
		var invalid *TextToolEnvelopePresentationError
		if err := probe.finish(); !errors.As(err, &invalid) || text.Len() != 0 {
			t.Fatalf("truncated function exposed or accepted: %v", err)
		}
	}
	var text strings.Builder
	probe := newModelTextToolEnvelopeProbe([]ToolSchema{{Name: "edit_file"}}, func(value string) error { text.WriteString(value); return nil })
	spaces := strings.Repeat(" ", maxTextToolEnvelopeProbeBytes+1)
	if err := probe.write(spaces); err != nil || len(probe.pending) > maxTextToolEnvelopeProbeBytes {
		t.Fatal("whitespace retained an unbounded opening probe")
	}
	if err := probe.write("<tool_call><function=edit_file><parameter=x>SYNTHETIC"); err == nil || probe.pending != "" || strings.Contains(text.String(), "SYNTHETIC") {
		t.Fatal("whitespace bypassed the tool envelope boundary")
	}
}

func TestTextToolEnvelopeDoesNotSuppressValidNativeActions(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, invalidArgs := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/invalidArgs=%t", streaming, invalidArgs), func(t *testing.T) {
				model := &malformedProgressActionModel{malformed: "<tool_call><function=inspect><parameter=x>SYNTHETIC</parameter></function></tool_call>", invalidArgs: invalidArgs}
				var client ModelClient = model
				if !streaming {
					client = nonStreamingProgressActionModel{model: model}
				}
				calls := 0
				var text strings.Builder
				engine := Engine{Model: client, Tools: FuncToolGateway(func(context.Context, ToolCall) (ToolResult, error) {
					calls++
					return ToolResult{Value: map[string]any{"ok": true}}, nil
				}), OnModelDelta: func(event ModelStreamEvent) error { text.WriteString(event.ContentDelta); return nil }}
				result, err := engine.Run(context.Background(), RunRequest{Messages: []Message{{Role: "user", Content: "Perform the authorized checks."}}, Tools: []ToolSchema{{Name: "inspect"}}, MaxToolRounds: 8})
				// Invalid calls are repaired privately by the existing admission
				// contract; rejection need not terminate the model's whole run.
				if strings.Contains(text.String(), "SYNTHETIC") || invalidArgs && calls != 0 ||
					!invalidArgs && (err != nil || calls != 5 || result.FinalMessage.Content != "Verified complete.") {
					t.Fatalf("native admission changed or text arguments exposed: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}
