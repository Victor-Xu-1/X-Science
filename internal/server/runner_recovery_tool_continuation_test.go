package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
)

func TestRecoveryControlTransitionDoesNotFreezeFurtherTools(t *testing.T) {
	for _, reason := range []string{
		sessionRunnerRequiredToolChoiceUnsatisfiedReasonCode,
		sessionRunnerPlanStepsIncompleteReasonCode,
		sessionRunnerPlanApprovalRequiredReasonCode,
		sessionRunnerRealScientificEvidenceRequiredReasonCode,
		sessionRunnerCompletionReviewRecoveryReasonCode,
		sessionRunnerResponseLanguageMismatchReasonCode,
		sessionRunnerFinalPresentationReasonCode,
		"artifact_reference_correction_required",
		"completion_review_correction_required",
		sessionRunnerVisualArtifactValidationReasonCode,
	} {
		t.Run(reason, func(t *testing.T) {
			run := &sessionRunnerChatRun{TaskIntent: "continue the existing task", CorrectionReason: reason}
			messages := []agentruntime.Message{
				{Role: "system", Content: sessionRunnerDurableCorrectionContextMarker},
				{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "load", Name: "skill", Arguments: json.RawMessage(`{"skill":"generic-worker"}`)}}},
				{Role: "tool", ToolCallID: "load", Content: `{"ok":true,"loaded":true}`},
			}
			gateway := serverAgentRuntimeToolGateway{taskRun: run}
			tools := []agentruntime.ToolSchema{{Name: "skill"}, {Name: "task_create"}}
			if sessionRunnerCorrectionReadyForRevalidation(run, messages) {
				t.Error("an intermediate recovery action froze the task as a completed candidate")
			}
			if choice := gateway.RequiredToolChoice(messages, tools); choice == "none" {
				t.Error("recovery forbade the next model-selected execution tool")
			}
		})
	}
}

// Only the external model is controlled: provider serialization, tool-choice
// enforcement, the server gateway and task persistence use real implementations.
func TestRecoveryProtocolContinuesThroughRealModelTransportAndGateway(t *testing.T) {
	for _, reason := range []string{sessionRunnerRequiredToolChoiceUnsatisfiedReasonCode, "artifact_reference_correction_required"} {
		t.Run(reason, func(t *testing.T) { testRecoveryContinuesThroughRealGateway(t, reason) })
	}
}

func TestInlinePublicationPreservesTaskExecution(t *testing.T) {
	for _, laterUserTurn := range []bool{false, true} {
		name := "preparation publication within task"
		if laterUserTurn {
			name = "historical publication before continuation"
		}
		t.Run(name, func(t *testing.T) {
			messages := []agentruntime.Message{
				{Role: "user", Content: "prepare the inputs and perform both authorized actions"},
				{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "draft", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["input.csv"]}`)}}},
				{Role: "tool", ToolCallID: "draft", Content: `{"ok":true,"completion_pending":true,"artifacts":[{"version_id":"draft"}]}`},
				{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "published", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["input.csv"]}`)}}},
				{Role: "tool", ToolCallID: "published", Content: `{"ok":true,"artifacts":[{"version_id":"valid-input"}]}`},
			}
			if laterUserTurn {
				messages = append(messages, agentruntime.Message{Role: "user", Content: "continue the remaining authorized work"})
			}
			testRecoveryContinuesThroughRealGateway(t, "", messages...)
		})
	}
}

func testRecoveryContinuesThroughRealGateway(t *testing.T, reason string, prior ...agentruntime.Message) {
	var requests atomic.Int64
	var toolsForbidden atomic.Bool
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		sequence := requests.Add(1)
		message := map[string]any{"role": "assistant", "content": "both actions completed"}
		if request["tool_choice"] == "none" {
			toolsForbidden.Store(true)
			message["content"] = "stopped before the second action"
		} else if sequence <= 2 {
			title := "recovery-first-action"
			if sequence == 2 {
				title = "recovery-next-action"
			}
			arguments, _ := json.Marshal(map[string]any{"title": title})
			message = map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": title, "type": "function", "function": map[string]any{
						"name": "task_create", "arguments": string(arguments),
					},
				}},
			}
		} else if sequence > 3 {
			t.Errorf("unexpected provider request %d", sequence)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}}); err != nil {
			t.Errorf("encode provider response: %v", err)
		}
	}))
	defer modelAPI.Close()

	srv := newV11TestServer(t, Options{FileRoot: t.TempDir()})
	run := &sessionRunnerChatRun{
		TaskIntent:       "perform both authorized actions",
		CorrectionReason: reason,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withTranscriptRunnerChatRun(ctx, run)
	schemas := srv.agentRuntimeToolSchemas([]string{"task_create"})
	engine := srv.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{
		Endpoint: modelAPI.URL + "/v1/chat/completions", Model: "controlled-recovery-provider",
		AllowedTools: []string{"task_create"}, RequestTimeout: 5 * time.Second, OutputLimitBytes: 64 * 1024,
	}, schemas)
	messages := []agentruntime.Message{
		{Role: "user", Content: "perform both authorized actions"},
		{Role: "system", Content: sessionRunnerDurableCorrectionContextMarker},
	}
	var initialChoice any = "required"
	if len(prior) > 0 {
		messages = prior
		initialChoice = sessionRunnerStatefulInitialToolChoice(engine, messages, schemas)
	}
	result, err := engine.Run(ctx, agentruntime.RunRequest{
		Messages: messages,
		Tools:    schemas, InitialToolChoice: initialChoice, MaxToolRounds: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolsForbidden.Load() || requests.Load() != 3 || result.FinalMessage.Content != "both actions completed" {
		t.Fatalf("recovery stopped early: forbidden=%v requests=%d final=%q", toolsForbidden.Load(), requests.Load(), result.FinalMessage.Content)
	}
	tasks, err := srv.executeTaskTool("task_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"recovery-first-action", "recovery-next-action"} {
		if !strings.Contains(string(raw), title) {
			t.Fatalf("real task persistence lacks %s: %s", title, raw)
		}
	}
}

func TestRecoveryRevalidationRequiresChangedPublication(t *testing.T) {
	for _, tc := range []struct {
		name, tool, result string
		ready              bool
	}{
		{"input read", "read_file", `{"ok":true,"content":"verified input"}`, false},
		{"environment setup", "manage_environments", `{"ok":true,"status":"ready"}`, false},
		{"empty publication", "save_artifacts", `{"ok":true,"artifacts":[]}`, false},
		{"unchanged publication", "save_artifacts", `{"ok":true,"artifacts":[{"version_id":"v1","unchanged":true}]}`, false},
		{"rejected publication", "save_artifacts", `{"ok":false,"artifacts":[{"version_id":"v2"}]}`, false},
		{"not executed", "save_artifacts", `{"ok":true,"executed":false,"artifacts":[{"version_id":"v2"}]}`, false},
		{"unversioned publication", "save_artifacts", `{"ok":true,"artifacts":[{"filename":"output.md"}]}`, false},
		{"changed publication", "save_artifacts", `{"ok":true,"artifacts":[{"version_id":"v2","unchanged":false}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &sessionRunnerChatRun{CorrectionReason: "artifact_reference_correction_required"}
			messages := []agentruntime.Message{
				{Role: "system", Content: sessionRunnerDurableCorrectionContextMarker},
				{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "action", Name: tc.tool, Arguments: json.RawMessage(`{}`)}}},
				{Role: "tool", ToolCallID: "action", Content: tc.result},
			}
			if got := sessionRunnerCorrectionReadyForRevalidation(run, messages); got != tc.ready {
				t.Fatalf("ready=%v want=%v", got, tc.ready)
			}
		})
	}
}
