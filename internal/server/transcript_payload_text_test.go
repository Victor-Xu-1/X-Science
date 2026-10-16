package server

import "testing"

func TestTranscriptPayloadTextPreservesAcceptedPlainTextBlocks(t *testing.T) {
	text := func(value any) map[string]any { return map[string]any{"type": "text", "text": value} }
	for _, test := range []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"string priority", map[string]any{"text": "first", "content": "second", "detail": "third"}, "first"},
		{"content string", map[string]any{"text": " ", "content": " original ", "detail": "fallback"}, " original "},
		{"one accepted block", map[string]any{"content": []any{text("**Finding:** exact.")}}, "**Finding:** exact."},
		{"blank text before blocks", map[string]any{"text": " ", "content": []any{text("answer")}}, "answer"},
		{"ordered exact blocks", map[string]any{"content": []any{text(" First\n"), text(""), text("结构 β ")}}, " First\n结构 β "},
		{"text before blocks", map[string]any{"text": "authoritative", "content": []any{text("not selected")}}, "authoritative"},
		{"blocks before detail", map[string]any{"content": []any{text("selected")}, "detail": "not selected"}, "selected"},
		{"blank blocks", map[string]any{"content": []any{text(" "), text("\n")}, "detail": "fallback"}, "fallback"},
		{"empty blocks", map[string]any{"content": []any{}, "message": "fallback"}, "fallback"},
		{"mixed tool blocks", map[string]any{"content": []any{text("must not leak a partial prefix"), map[string]any{"type": "tool_use", "text": "private"}}, "detail": "fallback"}, "fallback"},
		{"extra fields", map[string]any{"content": []any{map[string]any{"type": "text", "text": "private", "hidden": true}}}, ""},
		{"invalid text", map[string]any{"content": []any{text(42)}, "detail": "fallback"}, "fallback"},
		{"invalid block", map[string]any{"content": []any{nil}, "detail": "fallback"}, "fallback"},
		{"legacy detail priority", map[string]any{"detail": "detail", "message": "message"}, "detail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := transcriptPayloadText(test.payload); got != test.want {
				t.Fatalf("payload text=%q want=%q", got, test.want)
			}
		})
	}
}
