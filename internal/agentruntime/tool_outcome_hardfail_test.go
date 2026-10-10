package agentruntime

import "testing"

func TestHardFailedIsOnlyDefinitiveTerminalFailure(t *testing.T) {
	cases := []struct {
		name       string
		value      any
		failed     bool
		hardFailed bool
	}{
		{"success", map[string]any{"ok": true}, false, false},
		{"hard failure", map[string]any{"ok": false}, true, true},
		{"partial remains recoverable", map[string]any{"ok": false, "partial": true}, true, false},
		{"source unavailable remains recoverable", map[string]any{"sourceUnavailable": true}, true, false},
		{"typed source unavailable", testToolResultEnvelope{result: map[string]any{"sourceUnavailable": true}}, true, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := ClassifyToolResult(testCase.value)
			if outcome.Failed() != testCase.failed || outcome.HardFailed() != testCase.hardFailed {
				t.Fatalf("outcome=%q failed=%t hardFailed=%t", outcome, outcome.Failed(), outcome.HardFailed())
			}
		})
	}
}

func TestToolCallBatchErrorsStayBounded(t *testing.T) {
	if got := (*ToolCallBatchLimitError)(nil).Error(); got != "agent runtime tool call batch limit exceeded" {
		t.Fatalf("nil batch limit error = %q", got)
	}
	if got := (&ToolCallBatchLimitError{Limit: 8, Received: 12}).Error(); got != "agent runtime received 12 tool calls in one round; limit is 8" {
		t.Fatalf("batch limit error = %q", got)
	}
	if got := (*ToolCallBatchValidationError)(nil).Error(); got != "agent runtime tool call batch is invalid" {
		t.Fatalf("nil batch validation error = %q", got)
	}
	if got := (&ToolCallBatchValidationError{Index: 2, Code: "duplicate_id"}).Error(); got != "agent runtime tool call batch item 2 is invalid: duplicate_id" {
		t.Fatalf("batch validation error = %q", got)
	}
	if got := (*ToolRoundNoProgressError)(nil).Error(); got != "agent runtime tool call round made no semantic progress" {
		t.Fatalf("nil no-progress error = %q", got)
	}
	if got := (&ToolRoundNoProgressError{Limit: 3}).Error(); got != "agent runtime made no semantic progress for 3 consecutive identical tool call rounds" {
		t.Fatalf("no-progress error = %q", got)
	}
}
