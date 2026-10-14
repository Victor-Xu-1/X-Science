package server

import (
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestRunnerRecoveryMaterialResultUsesCanonicalOriginal(t *testing.T) {
	descriptor := map[string]any{
		"artifact_id": "controlled-artifact", "version_id": "controlled-version", "sha256": strings.Repeat("a", 64),
		"size_bytes": 8192, "content_type": "application/json", "outcome": "succeeded",
		"content_url": "/api/artifacts/controlled-artifact/versions/controlled-version", "preview": "{", "truncated": true,
	}
	for _, test := range []struct {
		name        string
		value       any
		restored    bool
		unavailable bool
		wantNil     bool
		outcome     agentruntime.ToolResultOutcome
	}{
		{"restored failure", map[string]any{"ok": false, "error": "controlled failure"}, true, false, false, agentruntime.ToolResultFailed},
		{"restored unavailable", map[string]any{"sourceUnavailable": true}, true, false, false, agentruntime.ToolResultUnavailable},
		{"restored computation", map[string]any{"ok": true, "stdout": "new computation"}, true, false, false, agentruntime.ToolResultSucceeded},
		{"missing original", descriptor, false, true, true, ""},
		{"unavailable overrides stale result", map[string]any{"ok": true}, true, true, true, ""},
		{"unrestored descriptor", descriptor, false, false, true, ""},
		{"ordinary inline computation", map[string]any{"ok": true, "stdout": "new computation"}, false, false, false, agentruntime.ToolResultSucceeded},
		{"ordinary domain artifact key", map[string]any{"artifact_id": "domain-id", "value": 1}, false, false, false, agentruntime.ToolResultSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := runnerRecoveryMaterialResult(test.value, test.restored, test.unavailable)
			if (actual == nil) != test.wantNil || actual != nil && agentruntime.ClassifyToolResult(actual) != test.outcome {
				t.Fatalf("storage descriptor or missing original changed execution outcome: %#v", actual)
			}
		})
	}
}
