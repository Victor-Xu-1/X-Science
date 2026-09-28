package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
)

func TestFileRepairKeepsInspectionAndExecutionAvailable(t *testing.T) {
	for _, code := range []string{"invalid_delimited_artifact", "invalid_json_artifact", "invalid_scientific_artifact", "unresolved_template_marker"} {
		t.Run(code, func(t *testing.T) {
			messages := []agentruntime.Message{
				runnerEvidenceDepthCall("save", "save_artifacts", map[string]any{"files": []string{"input.data"}}),
				{Role: "tool", ToolCallID: "save", Content: `{"ok":false,"code":"artifact_save_requires_correction","errors":[{"code":"` + code + `","path":"input.data"}]}`},
			}
			run := &sessionRunnerChatRun{TaskIntent: "inspect, repair and compute from the complete input"}
			gateway := serverAgentRuntimeToolGateway{taskRun: run}
			tools := []agentruntime.ToolSchema{{Name: "read_file"}, {Name: "repl"}, {Name: "edit_file"}, {Name: "save_artifacts"}}
			if choice := gateway.RequiredToolChoice(messages, tools); choice != "required" {
				t.Errorf("repair excludes model-selected inspection or computation: %#v", choice)
			}
			messages = append(messages,
				runnerEvidenceDepthCall("edit", "edit_file", map[string]any{"file_path": "input.data"}),
				agentruntime.Message{Role: "tool", ToolCallID: "edit", Content: `{"ok":true,"changed":true}`},
			)
			if choice := gateway.RequiredToolChoice(messages, tools); choice != "required" {
				t.Errorf("one edit excludes validation or remaining repair actions: %#v", choice)
			}
			if !sessionRunnerImmediateArtifactRepairRequired(run, messages) {
				t.Fatal("changed bytes alone incorrectly discharged file validation")
			}
		})
	}
}

// The model boundary is controlled; file parsing, workspace authorization,
// inspection, editing, publication and SQLite use their real implementations.
func TestFileRepairModelInspectsThenRepairsThroughRealGateway(t *testing.T) {
	fixture := newAgentSaveArtifactsFixture(t)
	const name = "out/input.csv"
	const broken = "item,value\nfirst,1\nsecond,2,extra\nthird,3\n"
	input := map[string]any{"files": []any{name}, "language": "text", "human_description": "Save repaired table"}
	write := writeAgentSaveArtifactsFile(t, fixture.projectPath, name, broken)
	fixture.saveExecution(t, fixture.identity.access, fixture.projectPath, "invalid-table", 1, write)
	rejected, err := fixture.server.executeAgentSaveArtifacts(fixture.toolContext(t, "save-invalid", input), fixture.identity, "save-invalid", input)
	if err == nil || len(agentSaveArtifactFailures(t, rejected)) != 1 {
		t.Fatalf("real parser did not reject malformed table: %#v %v", rejected, err)
	}
	assertAgentSaveArtifactsNoCanonicalWrites(t, fixture)
	raw, err := json.Marshal(rejected)
	if err != nil {
		t.Fatal(err)
	}
	messages := []agentruntime.Message{
		{Role: "user", Content: "inspect and repair the table without dropping records"},
		runnerEvidenceDepthCall("save-invalid", "save_artifacts", input),
		{Role: "tool", ToolCallID: "save-invalid", Content: string(raw)},
	}
	var requests atomic.Int64
	modelAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		sequence := requests.Add(1)
		message := map[string]any{"role": "assistant", "content": "all records repaired and published"}
		var toolName, callID string
		var arguments map[string]any
		switch sequence {
		case 1:
			if !hasChatToolNamed(anySliceValue(request["tools"]), "read_file") || request["tool_choice"] != "required" {
				t.Errorf("file repair hid inspection: choice=%#v", request["tool_choice"])
			}
			toolName, callID = "read_file", "inspect-table"
			arguments = map[string]any{"file_path": name, "human_description": "Inspect the malformed record"}
		case 2:
			providerMessages := anySliceValue(request["messages"])
			last := mapValue(providerMessages[len(providerMessages)-1])
			var observed map[string]any
			_ = json.Unmarshal([]byte(stringValue(last["content"])), &observed)
			sheets := anySliceValue(mapValue(observed["workbook"])["sheets"])
			var records []any
			if len(sheets) == 1 {
				records = anySliceValue(mapValue(sheets[0])["data"])
			}
			if len(records) != 4 || len(anySliceValue(records[2])) != 3 || stringValue(anySliceValue(records[2])[2]) != "extra" {
				t.Errorf("model did not receive actual malformed record: %#v", last)
			}
			toolName, callID = "edit_file", "repair-table"
			arguments = map[string]any{"file_path": name, "old_string": "second,2,extra", "new_string": "second,2", "human_description": "Repair record without deleting it"}
		case 3:
			toolName, callID, arguments = "save_artifacts", "save-fixed", input
		case 4:
			providerMessages := anySliceValue(request["messages"])
			last := mapValue(providerMessages[len(providerMessages)-1])
			if !strings.Contains(stringValue(last["content"]), "version_id") || strings.Contains(stringValue(last["content"]), "artifact_save_requires_correction") {
				t.Errorf("publication lacks successful immutable receipt: %#v", last)
			}
		default:
			t.Errorf("unexpected repair request %d", sequence)
		}
		if toolName != "" {
			encoded, _ := json.Marshal(arguments)
			message["tool_calls"] = []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": toolName, "arguments": string(encoded)}}}
			delete(message, "content")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}}); err != nil {
			t.Errorf("encode provider response: %v", err)
		}
	}))
	defer modelAPI.Close()
	run := &sessionRunnerChatRun{TaskIntent: "repair the entire table", SessionID: fixture.stream.FrameID}
	ctx, cancel := context.WithTimeout(fixture.toolContext(t, "save-fixed", input), 20*time.Second)
	defer cancel()
	ctx = withTranscriptRunnerChatRun(ctx, run)
	names := []string{"read_file", "edit_file", "save_artifacts"}
	schemas := fixture.server.agentKernelToolSchemas(fixture.identity, chatRunnerAllowedToolSet(names))
	engine := fixture.server.newAgentRuntimeEngineWithContext(ctx, SessionRunnerChatOptions{
		Endpoint: modelAPI.URL + "/v1/chat/completions", Model: "controlled-file-repair", AllowedTools: names,
		RequestTimeout: 5 * time.Second, OutputLimitBytes: 64 * 1024,
	}, schemas)
	gateway := engine.Tools.(serverAgentRuntimeToolGateway)
	gateway.kernel, gateway.sessionID, gateway.suppressHooks = fixture.identity, fixture.stream.FrameID, true
	engine.Tools = gateway
	result, err := engine.Run(ctx, agentruntime.RunRequest{Messages: messages, Tools: schemas,
		InitialToolChoice: sessionRunnerStatefulInitialToolChoice(engine, messages, schemas), MaxToolRounds: 4})
	if err != nil || requests.Load() != 4 || result.FinalMessage.Content != "all records repaired and published" {
		t.Fatalf("repair did not complete: requests=%d result=%#v err=%v", requests.Load(), result, err)
	}
	content, err := os.ReadFile(filepath.Join(fixture.projectPath, name))
	if err != nil || string(content) != strings.Replace(broken, "second,2,extra", "second,2", 1) {
		t.Fatalf("repair lost records: %q %v", content, err)
	}
}
