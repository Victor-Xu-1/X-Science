package server

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/toolgateway"
)

func TestKernelBatchAdmissionUsesPrecedingSetupReceipt(t *testing.T) {
	for _, tool := range []string{"python", "r", "bash"} {
		t.Run(tool, func(t *testing.T) {
			manager := kernelruntime.NewManager(kernelruntime.Config{CondaEnvsPath: filepath.Join(t.TempDir(), "envs")})
			f := newAgentSaveArtifactsFixtureWithKernelManager(t, manager)
			ctx := context.Background()
			run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
				Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, managedEnvironmentBindingsHydrated: true}
			ctx = withTranscriptRunnerChatRun(ctx, run)
			options := SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}
			language := tool
			if tool == "bash" {
				language = "python"
			}
			setupInput := map[string]any{"mode": "create", "name": "requested-env", "language": language, "packages": []any{"fixture"}, "resource_requirements": managedEnvironmentTestResources(), "human_description": "Prepare controlled runtime"}
			input := map[string]any{"environment": "requested-env", "code": "print(1)", "human_description": "Controlled calculation"}
			if tool == "bash" {
				delete(input, "code")
				input["command"] = "printf controlled"
			}
			calls := []agentruntime.ToolCall{{ID: "ordered-setup", Name: manageEnvironmentsToolName, Arguments: mustMarshalRawMessage(setupInput)}, {ID: "ordered-compute", Name: tool, Arguments: mustMarshalRawMessage(input)}}
			schemas := []agentruntime.ToolSchema{{Name: tool, Parameters: agentKernelLanguageToolParameters(language, "requested-env")}}
			if tool == "bash" {
				schemas = []agentruntime.ToolSchema{agentKernelBashToolSchema()}
			}
			gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity, sessionID: f.stream.SessionID, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas)}
			if err := f.server.checkpointChatModelToolCallsWithContext(ctx, options, run, calls, gateway); err != nil {
				t.Fatal(err)
			}
			if _, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, calls[1].ID); err != nil || found {
				t.Fatalf("premature executable approval found=%v err=%v", found, err)
			}
			if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{Type: agentruntime.EventToolStarted, ToolName: calls[0].Name, ToolCallID: calls[0].ID, Arguments: string(calls[0].Arguments)}); err != nil {
				t.Fatal(err)
			}
			authority := &recordingManagedEnvironmentAuthority{listResult: []kernelruntime.ManagedEnvironment{{Name: "verified-env", Language: language, Status: "ready", Generation: "verified-generation", Packages: []string{"fixture=1.0=build=conda"}}}}
			result, err := f.server.executeAgentEnvironmentManagementToolWithAuthority(ctx, f.identity, calls[0], manageEnvironmentsToolName, setupInput, authority)
			if err != nil || stringValue(mapValue(result)["mode"]) != "reuse" {
				t.Fatalf("setup=%#v err=%v", result, err)
			}
			run.bindManagedEnvironmentToolResult(manageEnvironmentsToolName, setupInput, result)
			if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{Type: agentruntime.EventToolCompleted, ToolName: calls[0].Name, ToolCallID: calls[0].ID, Arguments: string(calls[0].Arguments), Result: string(mustMarshalRawMessage(result))}); err != nil {
				t.Fatal(err)
			}
			if run.selectedManagedEnvironment("requested-env") != "verified-env" {
				t.Fatal("actual setup result did not bind its reused environment")
			}
			if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, agentruntime.Event{Type: agentruntime.EventToolStarted, ToolName: tool, ToolCallID: calls[1].ID, Arguments: string(calls[1].Arguments)}); err != nil {
				t.Fatal(err)
			}
			operation, found, err := f.store.GetKernelLocalOperationByToolCall(ctx, f.stream.OwnerID, f.stream.UID, calls[1].ID)
			if err != nil || !found || operation.Environment != "verified-env" || operation.ToolCallOrdinal != 1 {
				t.Fatalf("operation=%#v found=%v err=%v", operation, found, err)
			}
			// A later binding change must never rewrite the admitted input.
			run.bindManagedEnvironment("requested-env", "unapproved-env")
			execution := &serverAgentRuntimeGatewayExecution{gateway: gateway, call: calls[1]}
			invocation := toolgateway.NewInvocation(ctx, calls[1].ID, tool, calls[1].Arguments, execution)
			invocation.CanonicalName = tool
			serverAgentRuntimeGatewayAdmit(invocation)
			if invocation.Err != nil || !bytes.Equal(mustMarshalRawMessage(invocation.Input), operation.InputJSON) {
				t.Fatalf("admission=%#v err=%v", invocation.Input, invocation.Err)
			}
			if operation.State == workspace.KernelLocalOperationStatePendingApproval {
				operation, err = f.store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{OwnerUserID: f.stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "approve-ordered", Scope: "once", Source: "user", ActorID: f.stream.OwnerID, CurrentClaim: f.claim})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.server.authorizeAgentKernelLocalOperation(ctx, f.identity, calls[1], tool, invocation.Input, "verified-env"); err != nil {
				t.Fatalf("strict authorization rejected admitted operation: %v", err)
			}
			mutated := copyMapAny(input)
			mutated["environment"] = "unapproved-env"
			changed := calls[1]
			changed.Arguments = mustMarshalRawMessage(mutated)
			if _, handled, err := gateway.admittedKernelBatchInput(ctx, changed); !handled || err == nil {
				t.Fatal("changed incoming proposal reused an approval")
			}
			var canonicalInput map[string]any
			if err := json.Unmarshal(operation.InputJSON, &canonicalInput); err != nil {
				t.Fatal(err)
			}
			canonicalInput["environment"] = "unapproved-env"
			if _, err := f.server.authorizeAgentKernelLocalOperation(ctx, f.identity, calls[1], tool, canonicalInput, "unapproved-env"); err == nil || !strings.Contains(err.Error(), "conflicts") {
				t.Fatalf("approval identity/hash changed: %v", err)
			}
		})
	}
}
