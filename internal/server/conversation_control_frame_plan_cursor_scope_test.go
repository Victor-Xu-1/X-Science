package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestConversationControlSourceCheckpointKeepsPlanCursorTaskScoped(t *testing.T) {
	for _, row := range []struct {
		name         string
		typed        bool
		matching     bool
		legacy       bool
		contextOnly  bool
		kernelPolicy bool
		wantCursor   bool
	}{
		{name: "ordinary", wantCursor: true},
		{name: "foreign-control", typed: true},
		{name: "matching-control", typed: true, matching: true, wantCursor: true},
		{name: "no-run-legacy", legacy: true, wantCursor: true},
		{name: "foreign-control/context-only", typed: true, contextOnly: true},
		{name: "foreign-control-kernel-policy", typed: true, kernelPolicy: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			gateway, _ := conversationControlFramePlanScopeFixture(t, row.typed && !row.kernelPolicy)
			kernelMarker := ""
			if row.kernelPolicy {
				gateway, kernelMarker = admitControlPlanKernelSourceFixture(t, gateway)
			}
			server, run := gateway.server, gateway.taskRun
			if row.matching {
				bindPlanToCurrentControlForTest(t, gateway)
			}
			metadata, found, err := server.workspaceStore.GetFrameRuntimeMetadata(gateway.sessionID)
			if err != nil || !found {
				t.Fatalf("plan precondition found=%t err=%v", found, err)
			}
			if (run.currentControl != nil) != row.typed || sessionRunnerPlanMatchesTask(metadata.ContextData, run) != row.wantCursor {
				t.Fatal("fixture does not represent the requested admitted control/plan ownership")
			}
			metadata.ContextData[generatedPlanResearchLatestSourceEventIDKey] = int64(0)
			if _, err := server.workspaceStore.SetFrameRuntimeMetadata(gateway.sessionID, metadata); err != nil {
				t.Fatal(err)
			}
			before, _, err := server.workspaceStore.GetFrameRuntimeMetadata(gateway.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			call := agentruntime.ToolCall{ID: "control-source-cursor", Name: "web_search",
				Arguments: json.RawMessage(`{"query":"current-control-source"}`)}
			input := map[string]any{"query": "current-control-source"}
			result := map[string]any{"ok": true, "scope_marker": row.name,
				"sources": []any{map[string]any{"url": "https://example.test/current-source"}}}
			identity := "legacy-source-cursor"
			if row.kernelPolicy {
				call, result = checkpointControlPlanKernelSource(t, gateway, row.name, kernelMarker)
				identity = "kernel-mcp-evidence-" + call.ID
			} else if row.legacy || row.contextOnly {
				// The existing direct checkpoint entry supports both historical and contextual callers.
				checkpointContext := ctx
				if row.contextOnly {
					checkpointContext = withTranscriptRunnerChatRun(ctx, run)
				}
				if _, err := server.checkpointTranscriptRunnerEventWithDestinationsAndHook(checkpointContext, run.Transcript,
					transcriptstore.RunnerPhaseExecuting, identity, map[string]any{
						"status": "completed", "toolPhase": "completed", "toolCallId": call.ID,
						"toolName": call.Name, "toolInput": input, "toolResult": result,
					}, true, nil, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				run.ClaimToken = run.Transcript.Claim.ClaimToken
				options := SessionRunnerChatOptions{SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID}
				if err := server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}, gateway); err != nil {
					t.Fatal(err)
				}
				if err := server.checkpointChatTool(options, run, "running", "source started", call.ID, "start",
					map[string]any{"toolName": call.Name, "toolInput": input}); err != nil {
					t.Fatal(err)
				}
				if err := server.checkpointChatTool(options, run, "completed", "source completed", call.ID, "completed",
					map[string]any{"toolName": call.Name, "toolInput": input, "toolResult": result}); err != nil {
					t.Fatal(err)
				}
				identity = chatToolCheckpointPhase("completed-completed", call.ID)
			}
			event, persisted, err := server.transcriptStore.GetEventByClientMessageID(ctx,
				run.Transcript.Stream.UID, run.Transcript.Stream.OwnerID, transcriptRunnerClientMessageID(run.Transcript.Claim, identity))
			if err != nil || !persisted || event.EventID <= 0 {
				t.Fatalf("terminal source checkpoint was not persisted: found=%t id=%d err=%v", persisted, event.EventID, err)
			}
			var checkpoint sessionRunnerDurableToolCheckpoint
			if err := json.Unmarshal(event.PayloadJSON, &checkpoint); err != nil {
				t.Fatal(err)
			}
			encoded, valid := sessionRunnerDurableToolResult(checkpoint.ToolResult)
			var restored map[string]any
			if !valid || json.Unmarshal([]byte(encoded), &restored) != nil || !reflect.DeepEqual(restored, result) ||
				checkpoint.ToolCallID != call.ID || checkpoint.ToolName != call.Name || checkpoint.ToolPhase != "completed" {
				t.Fatalf("source checkpoint lost its current-task result: %#v result=%s", checkpoint, encoded)
			}
			if !row.legacy && !row.contextOnly && !row.kernelPolicy {
				items, err := server.workspaceStore.ListToolCallBatchItems(ctx, run.Transcript.Stream.OwnerID, run.ToolBatchIDs[call.ID])
				if err != nil || len(items) != 1 || items[0].TerminalEventID != event.EventID {
					t.Fatalf("source batch did not retain its terminal checkpoint: %#v err=%v", items, err)
				}
			}
			after, found, err := server.workspaceStore.GetFrameRuntimeMetadata(gateway.sessionID)
			if err != nil || !found {
				t.Fatalf("plan readback found=%t err=%v", found, err)
			}
			for _, key := range []string{"_plan_artifact_id", "_plan_version_id", "_plan_task_intent_id",
				"_plan_task_intent_revision", "_plan_task_intent_sha256", "_step_statuses"} {
				if !reflect.DeepEqual(before.ContextData[key], after.ContextData[key]) {
					t.Fatalf("source checkpoint rewrote plan field %s", key)
				}
			}
			want := int64(0)
			if row.wantCursor {
				want = event.EventID
			}
			if got := int64(numberValue(after.ContextData[generatedPlanResearchLatestSourceEventIDKey])); got != want {
				t.Errorf("source cursor crossed current-control task scope: got=%d want=%d terminal=%d", got, want, event.EventID)
			}
		})
	}
}

// Exercise the actual background host policy and its normal MCP evidence commit.
// Only a local synthetic JSON-RPC echo process runs; no kernel, model or network starts.
func admitControlPlanKernelSourceFixture(t *testing.T, gateway serverAgentRuntimeToolGateway) (serverAgentRuntimeToolGateway, string) {
	t.Helper()
	server, run := gateway.server, gateway.taskRun
	marker := filepath.Join(t.TempDir(), "synthetic-mcp-calls.log")
	program := writeKernelMCPStdioFixtureWithTool(t, marker, "search_articles")
	script, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, replacement := range [][2]string{
		{`"properties":{"text":{"type":"string"}}`, `"properties":{"query":{"type":"string"}}`},
		{`"required":["text"]`, `"required":["query"]`},
		{`["arguments"]["text"]`, `["arguments"]["query"]`},
	} {
		if strings.Count(text, replacement[0]) != 1 {
			t.Fatal("synthetic MCP fixture input contract changed")
		}
		text = strings.Replace(text, replacement[0], replacement[1], 1)
	}
	if err := os.WriteFile(program, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
	server.mcpDirectory.SetBundledPythonCommandProvider(func() (string, error) { return program, nil })
	// Resolve and enable the owner-scoped connector before the real input captures its authority.
	if _, err := server.mcpDirectory.SetUnifiedEnabled(t.Context(), run.Transcript.Stream.OwnerID, "bundled:pubmed", true); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := server.workspaceStore.SetMCPConnectorToolPolicy(run.Transcript.Stream.OwnerID, "bundled:pubmed", "search_articles", &enabled); err != nil {
		t.Fatal(err)
	}
	if _, err := server.workspaceStore.CreateAgent(workspace.CreateAgentInput{
		ID: "skill-control-agent", UserID: run.Transcript.Stream.OwnerID, Name: "OPERON", DisplayName: "Evidence assistant",
		Description: "Synthetic current-control source fixture", SystemPrompt: "PROFILE_PROMPT_MUST_SURVIVE",
		Unrestricted: true, Enabled: &enabled, SkillNames: []string{"customize", "skill-creator"},
	}); err != nil {
		t.Fatal(err)
	}
	fixture := &agentSaveArtifactsFixture{server: server, store: server.workspaceStore,
		repo: server.transcriptStore, stream: run.Transcript.Stream, claim: run.Transcript.Claim}
	finishSkillControlInput(t, fixture)
	submitSkillControlHTTPInput(t, fixture, "kernel-source-control", "CURRENT_CONTROL_SENTINEL", true)
	run = claimSkillControlInput(t, fixture)
	_, control, err := server.prepareConversationSkillContinuation(t.Context(), SessionRunnerChatOptions{}, run)
	if err != nil || control == nil {
		t.Fatalf("admitted kernel control=%#v err=%v", control, err)
	}
	run.currentControl = control
	id, revision := control.taskIdentity(run.Transcript.Stream.UID)
	if err := applySessionRunnerTaskIntent(run, control.instruction(), gateway.taskRun.ResponseLanguage, id, revision); err != nil {
		t.Fatal(err)
	}
	runtimeContext, found, err := server.workspaceMCPRuntimeContextWithContext(withTranscriptRunnerChatRun(t.Context(), run), gateway.sessionID)
	if err != nil || !found || runtimeContext.UserID != run.Transcript.Stream.OwnerID ||
		len(kernelMCPConnectorMatches(runtimeContext.Connectors, "pubmed")) != 1 {
		t.Fatalf("owner-scoped connector authority=%#v found=%t err=%v", runtimeContext, found, err)
	}
	run.setToolCapabilityCatalog(gateway.toolSchemas)
	gateway.taskRun = run
	return gateway, marker
}

func checkpointControlPlanKernelSource(t *testing.T, gateway serverAgentRuntimeToolGateway, markerValue, marker string) (agentruntime.ToolCall, map[string]any) {
	t.Helper()
	server, run := gateway.server, gateway.taskRun
	fixture := &agentSaveArtifactsFixture{server: server, store: server.workspaceStore,
		repo: server.transcriptStore, stream: run.Transcript.Stream, claim: run.Transcript.Claim}
	authority, _ := largeMCPStartedOperation(t, fixture)
	operation := authority.operation
	run.KernelOperationIDs = map[string]string{operation.ToolCallID: operation.OperationID}
	access, found, err := server.workspaceStore.GetKernelFrameAccessContext(t.Context(), gateway.sessionID)
	if err != nil || !found {
		t.Fatalf("kernel host access found=%t err=%v", found, err)
	}
	sourceContext := withTranscriptRunnerChatRun(t.Context(), run)
	binding := kernelTranscriptExecutionBindingFromContext(sourceContext, operation.ToolCallID,
		operation.KernelID, uint64(operation.KernelGeneration), nil)
	binding.currentKernelGeneration = func() uint64 { return uint64(operation.KernelGeneration) }
	policy := server.agentKernelExecutionHostPolicy(sourceContext, access, access.ProjectPath, []string{"mcp"}, false, binding)
	call := kernelruntime.HostCall{ID: "hc-00000000000000000000000000000051", CellID: operation.ExecutionID,
		Method: "mcp", Args: []any{"pubmed", "search_articles", map[string]any{"query": markerValue}}}
	callContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := policy.Handler(callContext, call)
	if err != nil {
		t.Fatalf("synthetic kernel host source checkpoint: %v", err)
	}
	var result map[string]any
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := output.(string); ok {
		encoded = []byte(value)
	}
	if json.Unmarshal(encoded, &result) != nil || result["echo"] != markerValue {
		t.Fatalf("synthetic host lost its result: %s", encoded)
	}
	calls, err := os.ReadFile(marker)
	if err != nil || string(calls) != "call\n" {
		t.Fatalf("synthetic tools/call marker=%q err=%v", calls, err)
	}
	return agentruntime.ToolCall{ID: call.ID, Name: "mcp__pubmed__search_articles"}, result
}
