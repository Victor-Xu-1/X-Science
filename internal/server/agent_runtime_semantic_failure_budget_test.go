package server

import (
	"encoding/json"
	"testing"

	"synon-go/internal/agentruntime"
	transcriptstore "synon-go/internal/persistence/transcript"
)

func semanticBudgetEvent(t *testing.T, value map[string]any) transcriptstore.RunnerReplayEvent {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return transcriptstore.RunnerReplayEvent{
		Event: transcriptstore.Event{Type: "runner_checkpoint"}, ResolvedPayloadJSON: payload,
	}
}

func TestDurableSemanticFailureTracksLatestExecutionIdentityWithoutClosingTarget(t *testing.T) {
	firstInput := map[string]any{"capability": "molecular-docking", "executable": "runner", "code": "first"}
	firstArguments, _ := json.Marshal(firstInput)
	failure := func(input map[string]any, diagnostic string) transcriptstore.RunnerReplayEvent {
		return semanticBudgetEvent(t, map[string]any{
			"type": "runner_checkpoint", "toolPhase": "failed", "status": "failed",
			"toolName": "software_runtime", "toolInput": input,
			"toolResult": map[string]any{
				"ok": false, "code": "nonzero_exit", "stderr": diagnostic,
			},
		})
	}
	projected := []transcriptstore.RunnerReplayEvent{failure(firstInput, "ValueError: first failure")}
	state := replayExecutionFailures(projected, "software_runtime", firstArguments)
	if executionFailureBoundary("software_runtime", firstArguments, state) == nil {
		t.Fatalf("first durable failure state=%#v", state)
	}
	projected = append(projected, semanticBudgetEvent(t, map[string]any{
		"type": "runner_checkpoint", "toolPhase": "completed", "status": "completed",
		"toolName": "Skill", "toolResult": map[string]any{"ok": true},
	}))
	state = replayExecutionFailures(projected, "software_runtime", firstArguments)
	if executionFailureBoundary("software_runtime", firstArguments, state) == nil {
		t.Fatalf("unrelated inspection changed execution failure state: %#v", state)
	}
	secondInput := map[string]any{"capability": "molecular-docking", "executable": "runner", "code": "corrected"}
	secondArguments, _ := json.Marshal(secondInput)
	projected = append(projected, failure(secondInput, "ValueError: corrected retry failed differently"))
	state = replayExecutionFailures(projected, "software_runtime", firstArguments)
	if executionFailureBoundary("software_runtime", firstArguments, state) == nil ||
		executionFailureBoundary("software_runtime", secondArguments, state) == nil {
		t.Fatalf("latest corrected execution identity was not retained: %#v", state)
	}
}

func TestDurableSemanticFailureIgnoresOtherTargetsAndTerminalBoundary(t *testing.T) {
	projected := []transcriptstore.RunnerReplayEvent{
		semanticBudgetEvent(t, map[string]any{
			"type": "runner_checkpoint", "toolPhase": "failed", "status": "failed",
			"toolName": "edit_file", "toolResult": map[string]any{
				"ok": false, "code": "nonzero_exit", "stderr": "ValueError: unrelated",
			},
		}),
		semanticBudgetEvent(t, map[string]any{
			"type": "runner_checkpoint", "toolPhase": "failed", "status": "failed",
			"toolName": "software_runtime", "toolInput": map[string]any{"capability": "other"},
			"toolResult": map[string]any{
				"ok": false, "code": "execution_path_exhausted", "message": "terminal boundary",
			},
		}),
	}
	arguments, _ := json.Marshal(map[string]any{"capability": "molecular-docking"})
	if state := replayExecutionFailures(projected, "software_runtime", arguments); executionFailureBoundary("software_runtime", arguments, state) != nil {
		t.Fatalf("unrelated or terminal failures consumed target state=%#v", state)
	}
}

func TestDurableSemanticFailureIgnoresNonExecutingPreflight(t *testing.T) {
	input := map[string]any{"code": `record = host.mcp("source-broker", "read", {})`}
	arguments, _ := json.Marshal(input)
	projected := []transcriptstore.RunnerReplayEvent{semanticBudgetEvent(t, map[string]any{
		"type": "runner_checkpoint", "toolPhase": prestartToolFailurePhase, "status": "failed",
		"toolName": "repl", "toolInput": input, "rejectedBeforeExecution": true,
		"toolResult": map[string]any{
			"ok": false, "executed": false, "status": "mcp_schema_preflight_required",
			"message": "load the exact connector contract",
		},
	})}
	if state := replayExecutionFailures(projected, "repl", arguments); executionFailureBoundary("repl", arguments, state) != nil {
		t.Fatalf("non-executing preflight consumed durable execution budget: %#v", state)
	}
}

func TestDurableEditConflictDoesNotPoisonExecutionRetry(t *testing.T) {
	input := map[string]any{"file_path": "report.md", "old_string": "stale", "new_string": "new"}
	arguments, _ := json.Marshal(input)
	failure := func(old string) transcriptstore.RunnerReplayEvent {
		return semanticBudgetEvent(t, map[string]any{
			"type": "runner_checkpoint", "toolPhase": "failed", "status": "failed",
			"toolName": "edit_file", "toolInput": map[string]any{
				"file_path": "report.md", "old_string": old, "new_string": "new",
			},
			"toolResult": map[string]any{
				"ok": false, "executed": false, "code": "edit_conflict", "retryable": true,
				"message": "The file changed or the requested old_string is not an exact unique match.",
			},
		})
	}
	projected := []transcriptstore.RunnerReplayEvent{failure("stale")}
	state := replayExecutionFailures(projected, "edit_file", arguments)
	if executionFailureBoundary("edit_file", arguments, state) != nil {
		t.Fatalf("non-executed edit conflict entered durable execution state=%#v", state)
	}
	projected = append(projected, semanticBudgetEvent(t, map[string]any{
		"type": "runner_checkpoint", "toolPhase": "completed", "status": "completed",
		"toolName": "read_file", "toolInput": map[string]any{"file_path": "report.md"},
		"toolResult": map[string]any{"ok": true, "content": "current"},
	}))
	state = replayExecutionFailures(projected, "edit_file", arguments)
	if executionFailureBoundary("edit_file", arguments, state) != nil {
		t.Fatalf("fresh read changed durable execution identity: %#v", state)
	}
	projected = append(projected, failure("current but mismatched"))
	state = replayExecutionFailures(projected, "edit_file", arguments)
	secondArguments, _ := json.Marshal(map[string]any{
		"file_path": "report.md", "old_string": "current but mismatched", "new_string": "new",
	})
	if executionFailureBoundary("edit_file", secondArguments, state) != nil {
		t.Fatalf("non-executed correction poisoned durable retry state=%#v", state)
	}
	projected = append(projected, semanticBudgetEvent(t, map[string]any{
		"toolName": "edit_file", "toolInput": input, "toolPhase": "failed", "status": "failed",
		"toolResult": map[string]any{"ok": false, "executed": true, "code": "workspace_write_failed", "error": "disk write failed", "changed": false},
	}))
	state = replayExecutionFailures(projected, "edit_file", arguments)
	if executionFailureBoundary("edit_file", arguments, state) == nil {
		t.Fatalf("real executed write failure was not retained: %#v", state)
	}
}

func TestExecutionIdentityIgnoresPresentationButDetectsMaterialCorrection(t *testing.T) {
	first := json.RawMessage(`{"environment":"python","code":"run()","human_description":"Running"}`)
	presentationOnly := json.RawMessage(`{"environment":"python","code":"run()","human_description":"Retrying"}`)
	corrected := json.RawMessage(`{"environment":"python","code":"run_fixed()","human_description":"Retrying"}`)
	firstIdentity := agentruntime.ExecutionCallFingerprint("python", first)
	if firstIdentity != agentruntime.ExecutionCallFingerprint("python", presentationOnly) {
		t.Fatal("presentation copy changed execution identity")
	}
	if firstIdentity == agentruntime.ExecutionCallFingerprint("python", corrected) {
		t.Fatal("corrected code did not change execution identity")
	}
}

func TestDurableSemanticBoundariesAreNonExecutingLifecycleEvents(t *testing.T) {
	arguments := json.RawMessage(`{"environment":"python","code":"run()"}`)
	for _, code := range []string{"python_execution_failed", "runtime_draining"} {
		var state agentruntime.ExecutionFailureLedger
		state.Observe(agentruntime.ToolCall{Name: "python", Arguments: arguments}, []string{"runtime-execution"},
			map[string]any{"ok": false, "executed": true, "code": code})
		boundary := executionFailureBoundary("python", arguments, state)
		if boundary["executed"] != false || boundary["preflight"] != true || !agentruntime.IsNonExecutingPreflight(boundary) {
			t.Fatalf("durable boundary was not marked as non-executing: %#v", boundary)
		}
		corrected := json.RawMessage(`{"environment":"python","code":"run_fixed()"}`)
		blocked := executionFailureBoundary("python", corrected, state) != nil
		if blocked != (code == "runtime_draining") {
			t.Fatalf("corrected execution decision: code=%s blocked=%t", code, blocked)
		}
	}
}
