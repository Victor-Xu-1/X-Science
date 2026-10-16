package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestOpenAIChatNativePreambleRequiresPositiveVisibleIdentity(t *testing.T) {
	for _, mode := range []string{"identified", "no-role", "late-role", "no-id", "late-id", "no-body", "private-only", "no-type", "no-call-id", "partial-name", "unadvertised"} {
		t.Run(mode, func(t *testing.T) {
			id, role, body, kind, callID, name := "response-one", "assistant", "I will inspect the records.", "function", "call-one", "inspect"
			switch mode {
			case "no-role", "late-role":
				role = ""
			case "no-id", "late-id":
				id = ""
			case "no-body", "private-only":
				body = ""
			case "no-type":
				kind = ""
			case "no-call-id":
				callID = ""
			case "partial-name":
				name = "insp"
			}
			first := map[string]any{"role": role, "content": body}
			if mode == "private-only" {
				first["reasoning_content"] = "Private reasoning is not a visible body."
			}
			payload := nativePreambleFixtureSSE(id, first, "")
			if mode == "late-id" {
				id = "response-one"
			}
			delta := map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": callID, "type": kind,
				"function": map[string]any{"name": name, "arguments": `{}`},
			}}}
			if mode == "late-role" {
				delta["role"] = "assistant"
			}
			payload += nativePreambleFixtureSSE(id, delta, "tool_calls") + "data: [DONE]\n\n"
			tools := []agentruntime.ToolSchema{{Name: "inspect"}}
			if mode == "unadvertised" {
				tools = nil
			}
			boundaries := 0
			_, _, _, _, err := readOpenAIChatStream(strings.NewReader(payload), 64<<10, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventNativePreambleBoundary {
					boundaries++
					if err := agentruntime.ValidateModelStreamEvent(event); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}, tools)
			if mode != "no-call-id" && err != nil {
				t.Fatalf("preamble eligibility changed original valid response: %v", err)
			}
			want := 0
			if mode == "identified" {
				want = 1
			}
			if boundaries != want {
				t.Fatalf("native boundaries=%d want=%d", boundaries, want)
			}
		})
	}
}

func TestOpenAIChatNativePreambleDoesNotFreezeLaterFunctionOrBodyFragments(t *testing.T) {
	payload := nativePreambleFixtureSSE("response-one", map[string]any{"role": "assistant", "content": "I will inspect the records."}, "")
	payload += nativePreambleFixtureSSE("response-one", map[string]any{"tool_calls": []any{map[string]any{
		"index": 0, "id": "call-one", "type": "function", "function": map[string]any{"name": "inspect", "arguments": `{"key":`},
	}}}, "")
	payload += nativePreambleFixtureSSE("response-one", map[string]any{"content": " A later draft remains decoder content.", "tool_calls": []any{map[string]any{
		"index": 0, "function": map[string]any{"name": "_value", "arguments": `"one"}`},
	}}}, "tool_calls") + "data: [DONE]\n\n"
	boundaries := 0
	response, _, _, _, err := readOpenAIChatStream(strings.NewReader(payload), 64<<10, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventNativePreambleBoundary {
			boundaries++
		}
		return nil
	}, []agentruntime.ToolSchema{{Name: "inspect"}, {Name: "inspect_value"}})
	if err != nil || boundaries != 1 || !strings.HasSuffix(response.Message.Content, " A later draft remains decoder content.") ||
		len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Name != "inspect_value" ||
		string(response.Message.ToolCalls[0].Arguments) != `{"key":"one"}` {
		t.Fatalf("prefix publication changed original decoder: boundaries=%d response=%+v error=%v", boundaries, response, err)
	}
}

func nativePreambleFixtureSSE(id string, delta map[string]any, finish string) string {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	encoded, _ := json.Marshal(map[string]any{"id": id, "choices": []any{choice}})
	return fmt.Sprintf("data: %s\n\n", encoded)
}
