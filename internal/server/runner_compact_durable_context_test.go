package server

import (
	"fmt"
	"strings"
	"testing"

	eventjournal "synon-go/internal/persistence/journal"
	sessionstore "synon-go/internal/persistence/sessions"
)

func TestCompactDurableContextDoesNotPromoteOneReceiptToTaskCompletion(t *testing.T) {
	for _, result := range []map[string]any{
		{"status": "idle", "num_notifications": 0},
		{"status": "completed", "ok": true},
		{"status": "already_loaded", "executed": false, "reused": true},
	} {
		entries := []eventjournal.Entry{{EventID: 7, Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolPhase": "completed", "toolCallId": "prepare-only",
			"toolName": "observation", "toolResult": result,
		}}}
		lines, err := compactDurableRuntimeContextLines(entries)
		if err != nil {
			t.Fatal(err)
		}
		context := strings.Join(lines, "\n")
		for _, forbidden := range []string{"Do not create a new inventory", "finalize the task", "release task resources"} {
			if strings.Contains(context, forbidden) {
				t.Fatalf("single receipt imposed task-level decision %q: %s", forbidden, context)
			}
		}
		if !strings.Contains(context, "does not establish task completion") || !strings.Contains(context, "prepare-only") {
			t.Fatalf("receipt scope was not preserved: %s", context)
		}
	}
}

func TestDeterministicCompactSummaryPreservesDurableFailureAndPendingRepair(t *testing.T) {
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{"type": "message", "role": "user", "text": "运行复杂科学任务并校验全部结果"}},
		{EventID: 2, Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolPhase": "start", "toolCallId": "call-runtime",
			"toolName": softwareRuntimeToolName,
			"toolInput": map[string]any{
				"provider": "local-conda", "working_dir": ".", "executable": "python",
				"args": []any{"pipeline.py"}, "expected_outputs": []any{map[string]any{"path": "output/validation.json"}},
			},
		}},
		{EventID: 3, Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolPhase": "failed", "toolCallId": "call-runtime",
			"toolName": softwareRuntimeToolName,
			"toolResult": map[string]any{
				"ok": false, "status": "failed", "code": "quality_contract_failed", "retryable": true,
				"recovery": "repair_the_result_basis_then_rerun_same_provider_plan",
				"stderr":   "comparison_contract_missing:output/results.csv:Cmax_Cmin_ratio",
			},
		}},
	}
	summary, err := buildCompactModelContextSummary(
		sessionstore.Session{ID: "frame-compact", Title: "compact test"}, entries, "preserve exact failure", "auto",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"quality_contract_failed", "comparison_contract_missing:output/results.csv:Cmax_Cmin_ratio",
		"repair_the_result_basis_then_rerun_same_provider_plan", "call-runtime", "Pending work",
	} {
		if !strings.Contains(summary, required) {
			t.Fatalf("deterministic compact summary is missing %q:\n%s", required, summary)
		}
	}

	successfulEntries := []eventjournal.Entry{
		{EventID: 11, Message: eventjournal.Message{
			"type": "runner_checkpoint", "toolPhase": "completed", "toolCallId": "call-validated",
			"toolName": softwareRuntimeToolName, "toolInput": map[string]any{
				"args": []any{"validate_report.py"}, "executable": "python",
			}, "toolResult": map[string]any{
				"ok": true, "status": "completed", "request_digest": strings.Repeat("a", 64),
				"outputs": []any{map[string]any{"path": "validate_report_result.json"}},
			},
		}},
	}
	successSummary, err := buildCompactModelContextSummary(
		sessionstore.Session{ID: "frame-compact-success", Title: "compact success test"}, successfulEntries, "", "auto",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(successSummary, "Continuation status") ||
		!strings.Contains(successSummary, "call-validated") ||
		!strings.Contains(successSummary, "does not establish task completion") ||
		!strings.Contains(successSummary, "continue unfinished work") {
		t.Fatalf("successful continuation state is missing:\n%s", successSummary)
	}
	prompt, err := buildCompactModelPrompt(
		sessionstore.Session{ID: "frame-compact", Title: "compact test"}, entries, "preserve exact failure", "auto",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## Durable Runtime State") ||
		!strings.Contains(prompt, "comparison_contract_missing:output/results.csv:Cmax_Cmin_ratio") {
		t.Fatalf("model compact prompt lost durable runtime state:\n%s", prompt)
	}
}

func TestDeterministicCompactSummaryRetainsRootTaskIntentBeyondRecentWindow(t *testing.T) {
	entries := []eventjournal.Entry{
		{EventID: 1, Message: eventjournal.Message{
			"type": "message", "role": "user", "text": "原始任务：完成完整的分子对接并保留可核验证据链",
		}},
	}
	for eventID := int64(2); eventID <= 60; eventID++ {
		role := "assistant"
		if eventID%2 == 0 {
			role = "user"
		}
		entries = append(entries, eventjournal.Entry{
			EventID: eventID,
			Message: eventjournal.Message{
				"type": "message", "role": role, "text": fmt.Sprintf("later turn %d", eventID),
			},
		})
	}

	summary, err := buildCompactModelContextSummary(
		sessionstore.Session{ID: "frame-root-intent", Title: "long task"}, entries, "", "auto",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "原始任务：完成完整的分子对接并保留可核验证据链") {
		t.Fatalf("root task intent was dropped from compact summary:\n%s", summary)
	}
}
