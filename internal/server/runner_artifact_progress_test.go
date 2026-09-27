package server

import (
	"encoding/json"
	"reflect"
	"testing"

	"synon-go/internal/agentruntime"
)

func TestRunnerPendingEvidenceRepairRequiresRealFileMutation(t *testing.T) {
	boundary := []agentruntime.Message{
		{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
			ID: "save-warning", Name: "save_artifacts",
			Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
		}}},
		{Role: "tool", ToolCallID: "save-warning", Content: `{
			"ok":true,
			"completion_pending":true,
			"warnings":[{
				"code":"unsupported_evidence_references",
				"path":"evidence.csv",
				"unsupported_evidence_references":[
					"evidence_source_locator_missing:evidence.csv row=2 source_type=evidence"
				]
			}]
		}`},
	}
	noOp := append(append([]agentruntime.Message(nil), boundary...),
		agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
			ID: "edit-no-op", Name: "edit_file",
			Arguments: json.RawMessage(`{"file_path":"evidence.csv","old_string":"same","new_string":"same"}`),
		}}},
		agentruntime.Message{Role: "tool", ToolCallID: "edit-no-op", Content: `{"ok":true,"changed":false}`},
	)
	paths, edited := runnerPendingArtifactEvidenceRepairMutation(noOp)
	if !reflect.DeepEqual(paths, []string{"evidence.csv"}) || edited {
		t.Fatalf("no-op repair state paths=%#v edited=%t", paths, edited)
	}

	changed := append(append([]agentruntime.Message(nil), boundary...),
		agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
			ID: "edit-changed", Name: "edit_file",
			Arguments: json.RawMessage(`{"file_path":"evidence.csv","old_string":"old","new_string":"new"}`),
		}}},
		agentruntime.Message{Role: "tool", ToolCallID: "edit-changed", Content: `{"ok":true,"changed":true}`},
	)
	paths, edited = runnerPendingArtifactEvidenceRepairMutation(changed)
	if !reflect.DeepEqual(paths, []string{"evidence.csv"}) || !edited {
		t.Fatalf("changed repair state paths=%#v edited=%t", paths, edited)
	}
}

func TestRunnerDurableCorrectionIgnoresNoOpEdit(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "system", Content: sessionRunnerDurableCorrectionContextMarker},
		{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
			ID: "edit-no-op", Name: "edit_file",
			Arguments: json.RawMessage(`{"file_path":"evidence.csv","old_string":"same","new_string":"same"}`),
		}}},
		{Role: "tool", ToolCallID: "edit-no-op", Content: `{"ok":true,"changed":false}`},
	}
	edited, saved := runnerSourceRepairMutationState(messages)
	if edited || saved {
		t.Fatalf("no-op durable correction edited=%t saved=%t", edited, saved)
	}
}

func TestRunnerInlineArtifactRepairKeepsToolsAvailableAfterCleanSave(t *testing.T) {
	gateway := serverAgentRuntimeToolGateway{taskRun: &sessionRunnerChatRun{TaskIntent: "prepare inputs and continue execution"}}
	tools := []agentruntime.ToolSchema{{Name: "edit_file"}, {Name: "save_artifacts"}}
	assertCanContinue := func(messages []agentruntime.Message) {
		t.Helper()
		if choice := gateway.RequiredToolChoice(messages, tools); choice == "none" {
			t.Fatal("an inline publication forbade the remaining task tools")
		}
	}
	warningCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "save-warning", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
	}}}
	warningResult := agentruntime.Message{Role: "tool", ToolCallID: "save-warning", Content: `{
		"ok":true,"completion_pending":true,
		"artifacts":[{"filename":"evidence.csv","version_id":"v1"}],
		"warnings":[{"code":"unsupported_evidence_references","path":"evidence.csv",
		"unsupported_evidence_references":["evidence_source_locator_missing:evidence.csv row=2 source_type=evidence"]}]
	}`}
	editCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "edit", Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"evidence.csv"}`),
	}}}
	editResult := agentruntime.Message{Role: "tool", ToolCallID: "edit", Content: `{"ok":true,"changed":true}`}
	cleanSaveCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "save-clean", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
	}}}
	cleanSaveResult := agentruntime.Message{Role: "tool", ToolCallID: "save-clean", Content: `{
		"ok":true,"artifacts":[{"filename":"evidence.csv","version_id":"v2","unchanged":true}]
	}`}
	messages := []agentruntime.Message{warningCall, warningResult, editCall, editResult, cleanSaveCall, cleanSaveResult}
	assertCanContinue(messages)

	blockedSaveCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "save-blocked", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
	}}}
	blockedSaveResult := agentruntime.Message{Role: "tool", ToolCallID: "save-blocked", Content: `{
		"ok":false,"executed":false,"preflight":true,"code":"repeated_non_progressing_tool_call"
	}`}
	assertCanContinue(append(messages, blockedSaveCall, blockedSaveResult))

	laterEditCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "edit-later", Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"evidence.csv"}`),
	}}}
	laterEditResult := agentruntime.Message{Role: "tool", ToolCallID: "edit-later", Content: `{"ok":true,"changed":true}`}
	assertCanContinue(append(messages, laterEditCall, laterEditResult))

	normalSave := []agentruntime.Message{cleanSaveCall, cleanSaveResult}
	assertCanContinue(normalSave)
}

func TestRunnerAdvisoryEvidenceWarningDoesNotOpenCorrectionWindow(t *testing.T) {
	warningCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "save-advisory", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
	}}}
	warningResult := agentruntime.Message{Role: "tool", ToolCallID: "save-advisory", Content: `{
		"ok":true,
		"artifacts":[{"filename":"evidence.csv","version_id":"v1"}],
		"warnings":[{"code":"unsupported_evidence_references","path":"evidence.csv",
		"blocking":false,"quality_advisory":true,
		"unsupported_evidence_references":["pmid:24893891"]}]
	}`}
	cleanSaveCall := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "save-later", Name: "save_artifacts", Arguments: json.RawMessage(`{"files":["evidence.csv"]}`),
	}}}
	cleanSaveResult := agentruntime.Message{Role: "tool", ToolCallID: "save-later", Content: `{
		"ok":true,"artifacts":[{"filename":"evidence.csv","version_id":"v1","unchanged":true}]
	}`}
	messages := []agentruntime.Message{warningCall, warningResult, cleanSaveCall, cleanSaveResult}
	gateway := serverAgentRuntimeToolGateway{taskRun: &sessionRunnerChatRun{TaskIntent: "continue the task"}}
	if choice := gateway.RequiredToolChoice(messages, []agentruntime.ToolSchema{{Name: "save_artifacts"}}); choice != nil {
		t.Fatalf("advisory warning constrained tool selection: %#v", choice)
	}
	if paths, edited := runnerPendingArtifactEvidenceRepairMutation(messages[:2]); len(paths) != 0 || edited {
		t.Fatalf("advisory warning opened an artifact repair path: paths=%#v edited=%t", paths, edited)
	}
}
