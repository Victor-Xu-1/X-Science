package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

type orderedKernelBatchModel struct {
	calls    []agentruntime.ToolCall
	requests int
}

func (m *orderedKernelBatchModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	m.requests++
	if m.requests == 1 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: m.calls}}, nil
	}
	if m.requests == 2 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "The controlled calculation completed."}}, nil
	}
	return agentruntime.ModelResponse{}, errors.New("unexpected repair/model repetition")
}

type orderedKernelBatchGateway struct {
	serverAgentRuntimeToolGateway
	authority managedEnvironmentAuthority
}

func (g orderedKernelBatchGateway) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if call.Name != manageEnvironmentsToolName {
		return g.serverAgentRuntimeToolGateway.Execute(ctx, call)
	}
	var input map[string]any
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return agentruntime.ToolResult{}, err
	}
	result, err := g.server.executeAgentEnvironmentManagementToolWithAuthority(ctx, g.kernel, call, call.Name, input, g.authority)
	if err == nil {
		g.taskRun.bindManagedEnvironmentToolResult(call.Name, input, result)
	}
	return agentruntime.ToolResult{Value: result}, err
}

// The environment provider is controlled; Engine admission/preflight, actual
// reuse producer, lifecycle checkpoints, approval, Python and receipts are real.
func TestKernelBatchAdmissionEngineRunsSetupThenPythonWithoutRepair(t *testing.T) {
	manager := newServerKernelTestManagerWithPythonEnvironment(t, "python")
	if !manager.ManagedPythonCapabilityAvailable() || !manager.RuntimeReady("python", "python") {
		t.Fatal("ordered execution regression requires an advertised, ready Python runtime")
	}
	f := newAgentSaveArtifactsFixtureWithKernelManager(t, manager)
	seedPermissionTestAgent(t, f.store, f.stream.OwnerID)
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.FrameID, workspace.FrameRuntimeMetadata{FrameID: f.stream.FrameID, ContextData: map[string]any{"web_assistant": map[string]any{"id": "xscience:operon", "conversation_overrides": map[string]any{"permission": "allow"}}}}); err != nil {
		t.Fatal(err)
	}
	if mode, err := f.server.webSessionApprovalMode(f.stream.FrameID); err != nil || mode != "allow" {
		t.Fatalf("controlled conversation approval authority mode=%q err=%v", mode, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, managedEnvironmentBindingsHydrated: true}
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	schemas := append(agentEnvironmentManagementToolSchemas(), agentruntime.ToolSchema{Name: "python", Parameters: agentKernelLanguageToolParameters("Python", "python")})
	gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity, sessionID: f.stream.SessionID, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas), hasToolSnapshot: true}
	ready := kernelruntime.ManagedEnvironment{Name: "python", Language: "python", Status: "ready", Generation: "controlled-ready", Packages: []string{"fixture=1.0=build=conda"}}
	// Discovery and the mandatory reuse-boundary inspection describe the same runtime.
	authority := &recordingManagedEnvironmentAuthority{listResult: []kernelruntime.ManagedEnvironment{ready}, mutateResult: ready}
	model := &orderedKernelBatchModel{calls: []agentruntime.ToolCall{
		{ID: "engine-setup", Name: manageEnvironmentsToolName, Arguments: mustMarshalRawMessage(map[string]any{"mode": "create", "name": "requested-env", "language": "python", "packages": []any{"fixture"}, "resource_requirements": managedEnvironmentTestResources(), "human_description": "Prepare controlled runtime"})},
		{ID: "engine-compute", Name: "python", Arguments: mustMarshalRawMessage(map[string]any{"environment": "requested-env", "code": "with open('ordered-engine-count.txt', 'a') as output:\n    output.write('x')\nprint(17)", "human_description": "Run controlled calculation"})},
	}}
	options := SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}
	engine := agentruntime.Engine{Model: model, Tools: orderedKernelBatchGateway{serverAgentRuntimeToolGateway: gateway, authority: authority}}
	results := make(map[string]string)
	engine.OnEventError = func(event agentruntime.Event) error {
		if event.Type == agentruntime.EventToolCompleted || event.Type == agentruntime.EventToolFailed {
			results[event.ToolCallID] = event.Result
		}
		if event.Type == agentruntime.EventModelResponse && len(event.ToolCalls) > 0 {
			return f.server.checkpointChatModelToolCallsWithContext(ctx, options, run, event.ToolCalls, gateway)
		}
		return f.server.checkpointSessionRunnerToolEvent(ctx, options, run, event)
	}
	if _, err := engine.Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Prepare an environment and run the controlled calculation."}}, Tools: schemas, MaxToolRounds: 3}); err != nil {
		t.Fatal(err)
	}
	if model.requests != 2 {
		t.Fatalf("repair/model requests=%d", model.requests)
	}
	var setup map[string]any
	if err := json.Unmarshal([]byte(results["engine-setup"]), &setup); err != nil || stringValue(setup["mode"]) != "reuse" || authority.inspectName != ready.Name || authority.mutations != 0 {
		t.Fatalf("setup=%s inspected=%q mutations=%d err=%v", results["engine-setup"], authority.inspectName, authority.mutations, err)
	}
	operation, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, "engine-compute")
	if err != nil || !found || operation.Environment != "python" || operation.State != workspace.KernelLocalOperationStateCompleted || operation.ApprovalSource != "policy" {
		t.Fatalf("operation=%q state=%q reason=%q environment=%q approval=%q found=%v err=%v tool_results=%v", operation.OperationID, operation.State, operation.ReasonCode, operation.Environment, operation.ApprovalSource, found, err, results)
	}
	contents, err := os.ReadFile(filepath.Join(f.projectPath, "ordered-engine-count.txt"))
	if err != nil || string(contents) != "x" {
		t.Fatalf("exactly-once output=%q err=%v tool_results=%v", contents, err, results)
	}
}
