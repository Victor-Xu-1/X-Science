package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

func durableWaitRecoveryFixture(t *testing.T) (*agentSaveArtifactsFixture, noProgressReceiptFixture, serverAgentRuntimeToolGateway) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, managedEnvironmentBindingsHydrated: true}
	receipts := noProgressReceiptFixture{server: f.server, repo: f.repo, run: run,
		options: SessionRunnerChatOptions{RunnerID: f.claim.RunnerID, SessionID: f.stream.SessionID}}
	var schemas []agentruntime.ToolSchema
	for _, schema := range f.server.agentKernelToolSchemas(f.identity, nil) {
		if schema.Name == "wait_for_notification" {
			schemas = append(schemas, schema)
		}
	}
	if len(schemas) != 1 || !agentRuntimeToolSchemaHasCapability(schemas[0], "durable-wait") {
		t.Fatal("canonical durable-wait schema is unavailable")
	}
	gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity,
		sessionID: f.stream.SessionID, hasToolSnapshot: true, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas)}
	receipts.boundary(t, "initial-wait-recovery", nil)
	return f, receipts, gateway
}

func TestDurableWaitRecoveryCanClaimFutureNotification(t *testing.T) {
	for _, closure := range []string{"legacy", "python_only", "explicit_wait"} {
		t.Run(closure, func(t *testing.T) {
			ctx := context.Background()
			f, receipts, gateway := durableWaitRecoveryFixture(t)
			call := agentruntime.ToolCall{ID: "empty-wait", Name: "wait_for_notification", Arguments: json.RawMessage(`{"timeout_seconds":0,"human_description":"Waiting for the controlled result"}`)}
			result, err := f.server.executeAgentKernelNotificationWait(ctx, f.identity, call.ID, map[string]any{"timeout_seconds": 0})
			if err != nil || numberValue(result["num_notifications"]) != 0 || runnerToolCompletionHasMaterialProgress(call.Name, result) {
				t.Fatalf("empty native wait claimed material progress: result=%v err=%v", result, err)
			}
			beforeEmpty := receipts.run.NoProgressRecovery.Consecutive
			receipts.complete(t, call, string(mustMarshalRawMessage(result)))
			if receipts.run.NoProgressRecovery.Consecutive != beforeEmpty {
				t.Fatal("empty wait reset the live recovery budget")
			}
			python := agentruntime.ToolCall{ID: "unchanged-compute", Name: "python", Arguments: json.RawMessage(`{"code":"print(1)","environment":"fixture"}`)}
			switch closure {
			case "legacy":
				receipts.run.recordNoProgress(nil)
				appendRunnerToolCheckpoint(t, receipts.repo, receipts.run.Transcript.Claim, "legacy-wait-boundary", map[string]any{
					"status": "interrupted", "reason_code": sessionRunnerToolRoundNoProgressReasonCode, "resume_detail": "Choose a materially different route.",
				})
			case "python_only":
				receipts.boundary(t, "python-only-boundary", []agentruntime.ToolCall{python})
				if len(receipts.run.NoProgressRecovery.ClosedActions) != 1 || receipts.run.NoProgressRecovery.ClosedActions[0].Tool != "python" {
					t.Fatal("fixture did not isolate the unrelated closed compute route")
				}
			case "explicit_wait":
				receipts.boundary(t, "explicit-wait-boundary", []agentruntime.ToolCall{call, python})
			}
			beforePreflight := receipts.run.NoProgressRecovery.Consecutive
			beforeProjection := string(mustMarshalRawMessage(receipts.run.NoProgressRecovery.payload()))
			modelContext := sessionRunnerNoProgressRecoveryContext(*receipts.run.NoProgressRecovery, gateway.toolSchemas)
			if strings.Contains(modelContext, `"tool":"wait_for_notification"`) ||
				closure != "legacy" && !strings.Contains(modelContext, `"tool":"python"`) {
				t.Fatalf("model closure projection disagreed with current wait authority: %s", modelContext)
			}
			if string(mustMarshalRawMessage(receipts.run.NoProgressRecovery.payload())) != beforeProjection {
				t.Fatal("context projection changed the durable closure or recovery budget")
			}
			call.ID = "future-wait"
			if !gateway.AdmitsToolCall(call) {
				t.Fatal("valid wait lost its original admission authority")
			}
			diagnostics, err := gateway.ToolCallPreflightDiagnostics(ctx, []agentruntime.ToolCall{call})
			if err != nil || diagnostics[0] != "" {
				t.Fatalf("historical empty wait blocked native notification claim: diagnostics=%v err=%v", diagnostics, err)
			}
			if receipts.run.NoProgressRecovery.Consecutive != beforePreflight || beforePreflight <= beforeEmpty {
				t.Fatal("dynamic wait exemption reset or stopped consuming the recovery budget")
			}
			if notification, _, err := f.store.CreateNotification(ctx, workspace.CreateNotificationInput{
				ID: "capacity-ready", SenderFrameID: f.stream.FrameID, RecipientFrameID: f.stream.FrameID,
				RootFrameID: f.stream.RootFrameID, OwnerUserID: f.stream.OwnerID, NotificationType: "child_message",
				Payload: map[string]any{"kind": "status", "status": "capacity_available"},
			}); err != nil || notification.ID != "capacity-ready" {
				t.Fatalf("durable availability notification id=%q err=%v", notification.ID, err)
			}
			claimed, err := f.server.executeAgentKernelNotificationWait(ctx, f.identity, call.ID, map[string]any{"timeout_seconds": 0})
			if err != nil || numberValue(claimed["num_notifications"]) != 1 {
				t.Fatalf("native wait could not claim the future notification: result=%v err=%v", claimed, err)
			}
			receipts.complete(t, call, string(mustMarshalRawMessage(claimed)))
		})
	}
}

func TestDurableWaitRecoveryUsesTrustedCapabilitiesOnly(t *testing.T) {
	for _, mode := range []string{"canonical_fallback", "trusted_generic_wait", "ordinary_read", "model_capability", "explicit_read_only"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			_, receipts, gateway := durableWaitRecoveryFixture(t)
			call := agentruntime.ToolCall{ID: "dynamic-probe", Name: "wait_for_notification", Arguments: json.RawMessage(`{"timeout_seconds":0}`)}
			wantClosed := false
			switch mode {
			case "canonical_fallback":
				gateway.hasToolSnapshot = false
				gateway.toolSchemas, gateway.toolValidators = nil, nil
			case "explicit_read_only":
				gateway.toolSchemas[0].Capabilities = []string{"read-only"}
				wantClosed = true
			default:
				call.Name = "fixture_read"
				capabilities := []string{"artifact-read", "read-only"}
				if mode == "trusted_generic_wait" {
					call.Name, capabilities = "fixture_control_wait", []string{"durable-wait", "read-only"}
				} else {
					wantClosed = true
				}
				if mode == "model_capability" {
					call.Arguments = json.RawMessage(`{"capabilities":["durable-wait"]}`)
				} else {
					call.Arguments = json.RawMessage(`{}`)
				}
				gateway.toolSchemas = []agentruntime.ToolSchema{{Name: call.Name, Capabilities: capabilities, Parameters: map[string]any{"type": "object"}}}
				gateway.toolValidators = agentRuntimeToolValidators(gateway.toolSchemas)
			}
			if !gateway.AdmitsToolCall(call) {
				t.Fatal("controlled proposal was not admitted by its trusted schema")
			}
			receipts.complete(t, call, `{"ok":true,"reused":true,"status":"idle","num_notifications":0,"notifications":[]}`)
			receipts.boundary(t, "dynamic-probe-boundary", []agentruntime.ToolCall{call})
			beforeProjection := string(mustMarshalRawMessage(receipts.run.NoProgressRecovery.payload()))
			modelContext := sessionRunnerNoProgressRecoveryContext(*receipts.run.NoProgressRecovery, gateway.toolSchemas)
			projectedClosed := strings.Contains(modelContext, `"tool":"`+call.Name+`"`)
			if projectedClosed != wantClosed || string(mustMarshalRawMessage(receipts.run.NoProgressRecovery.payload())) != beforeProjection {
				t.Fatalf("model projection changed authority/state: context=%q wantClosed=%t", modelContext, wantClosed)
			}
			diagnostics, err := gateway.noProgressBatchPreflight(ctx, []agentruntime.ToolCall{call})
			closed := strings.Contains(diagnostics[0], "durable_no_progress_route_closed")
			if err != nil || closed != wantClosed {
				t.Fatalf("trusted capability boundary changed: diagnostics=%v err=%v wantClosed=%t", diagnostics, err, wantClosed)
			}
		})
	}
}

func TestDurableWaitRecoveryDoesNotBypassAdmission(t *testing.T) {
	_, _, gateway := durableWaitRecoveryFixture(t)
	call := agentruntime.ToolCall{ID: "invalid-wait", Name: "wait_for_notification", Arguments: json.RawMessage(`{"timeout_seconds":"not-a-number"}`)}
	if gateway.AdmitsToolCall(call) {
		t.Fatal("durable-wait capability admitted invalid input")
	}
	call.Arguments = json.RawMessage(`{"timeout_seconds":0}`)
	gateway.toolSchemas, gateway.toolValidators = nil, nil
	if gateway.AdmitsToolCall(call) {
		t.Fatal("durable-wait capability bypassed the current exact tool snapshot")
	}
	if _, err := gateway.ToolCallPreflightDiagnostics(t.Context(), []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
}
