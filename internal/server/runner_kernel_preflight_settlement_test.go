package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"synon-go/internal/agentruntime"
	transcriptstore "synon-go/internal/persistence/transcript"
	workspace "synon-go/internal/persistence/workspace"
)

func TestKernelPreflightSettlementRequiresNonExecutionProof(t *testing.T) {
	for _, test := range []struct {
		name   string
		result map[string]any
		want   string
	}{
		{"explicit status", map[string]any{"ok": false, "executed": false, "preflight": true, "status": "input_authority_required", "message": "input lineage is missing"}, "input_authority_required"},
		{"explicit code", map[string]any{"ok": false, "executed": false, "preflight": true, "code": "repeated_failed_tool_call", "message": "the call is unchanged"}, "repeated_failed_tool_call"},
		{"explicit without code", map[string]any{"ok": false, "executed": false, "preflight": true, "message": "admission rejected"}, "execution_preflight_required"},
		{"executed status", map[string]any{"ok": false, "executed": true, "status": "code_preflight_required"}, ""},
		{"unknown provenance", map[string]any{"ok": false, "preflight": true, "code": "input_authority_required", "message": "admission rejected"}, ""},
		{"legacy without provenance", map[string]any{"ok": false, "status": "code_preflight_required"}, ""},
		{"ordinary execution failure", map[string]any{"ok": false, "code": "process_failed", "message": "process failed"}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := agentKernelPreflightReasonCode(test.result); got != test.want {
				t.Fatalf("reason=%q want=%q", got, test.want)
			}
		})
	}
}

func TestRecoveredExplicitKernelPreflightPreservesLogicalOutcome(t *testing.T) {
	for _, succeeded := range []bool{true, false} {
		status, phase := recoveredKernelToolCheckpointStatus(workspace.KernelLocalOperation{
			State: workspace.KernelLocalOperationStateCancelled, ReasonCode: "admission_decision",
		}, map[string]any{
			"ok": succeeded, "executed": false, "preflight": true,
			"status": "admission_decision", "message": "the admission decision is settled",
		})
		want := "failed"
		if succeeded {
			want = "completed"
		}
		if status != want || phase != want {
			t.Fatalf("succeeded=%t status=%q phase=%q want=%q", succeeded, status, phase, want)
		}
	}
}

// Exercise the public event settlement against real transcript and operation
// tables. A closed pre-execution rejection must not leave an approved job for
// restart recovery or keep deployment liveness artificially nonzero.
func TestCheckpointRejectedKernelOperationSettlesWithoutExecution(t *testing.T) {
	store, repo, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, "local", "project-rejection-settlement", "frame-rejection-settlement")
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
		FrameID: "frame-rejection-settlement", MessageUUID: "rejection-message",
		ClientMessageID: "rejection-client", Text: "execute the validated input",
	}); err != nil {
		t.Fatal(err)
	}
	stream, found, err := repo.GetFrameStreamBySession(context.Background(), "local", "frame-rejection-settlement")
	if err != nil || !found {
		t.Fatalf("stream found=%t err=%v", found, err)
	}
	claimed, err := repo.ClaimRunner(context.Background(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "rejection-runner",
		TTL: 5 * time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	run := &sessionRunnerChatRun{
		SessionID: stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim},
	}
	options := SessionRunnerChatOptions{RunnerID: claimed.Claim.RunnerID}
	for _, test := range []struct {
		name     string
		approved bool
		event    agentruntime.EventType
		result   string
		reason   string
	}{
		{"failed-legacy-approved", true, agentruntime.EventToolFailed, `{"ok":false,"executed":false,"status":"code_preflight_required","message":"code needs repair"}`, "code_preflight_required"},
		{"failed-explicit-approved", true, agentruntime.EventToolFailed, `{"ok":false,"executed":false,"preflight":true,"code":"repeated_failed_tool_call","message":"the call is unchanged"}`, "repeated_failed_tool_call"},
		{"completed-explicit-approved", true, agentruntime.EventToolCompleted, `{"ok":false,"executed":false,"preflight":true,"status":"input_authority_required","message":"input lineage is missing"}`, "input_authority_required"},
		{"failed-explicit-pending", false, agentruntime.EventToolFailed, `{"ok":false,"executed":false,"preflight":true,"code":"input_authority_required","message":"input lineage is missing"}`, "input_authority_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := agentruntime.ToolCall{ID: test.name, Name: "python", Arguments: json.RawMessage(`{"code":"print(1)","environment":"synon-biomed-python"}`)}
			if err := server.checkpointChatModelToolCalls(options, run, []agentruntime.ToolCall{call}); err != nil {
				t.Fatal(err)
			}
			operationID := run.KernelOperationIDs[call.ID]
			operation, found, err := store.GetKernelLocalOperation(context.Background(), stream.OwnerID, operationID)
			if err != nil || !found {
				t.Fatalf("operation found=%t err=%v", found, err)
			}
			if test.approved {
				_, err = store.ResolveKernelLocalOperationApproval(context.Background(), workspace.ResolveKernelLocalOperationApprovalInput{
					OwnerUserID: stream.OwnerID, OperationID: operationID,
					ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID,
					Approved: true, DecisionID: "decision-" + test.name, Scope: "once",
					Source: "policy", ActorID: "system", CurrentClaim: claimed.Claim,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			event := agentruntime.Event{
				Type: test.event, ToolName: call.Name, ToolCallID: call.ID,
				Arguments: string(call.Arguments), Result: test.result, RejectedBeforeExecution: true,
			}
			for replay := 0; replay < 2; replay++ {
				if err := server.checkpointSessionRunnerToolEvent(context.Background(), options, run, event); err != nil {
					t.Fatal(err)
				}
			}
			operation, found, err = store.GetKernelLocalOperation(context.Background(), stream.OwnerID, operationID)
			if err != nil || !found || operation.State != workspace.KernelLocalOperationStateCancelled ||
				operation.ReasonCode != test.reason || operation.ExecutionID != "" || operation.PreparedAt != nil || operation.StartedAt != nil {
				t.Fatalf("unsettled rejection: operation=%#v found=%t err=%v", operation, found, err)
			}
			if operation.ApprovalDecision != map[bool]string{true: "allow", false: "deny"}[test.approved] {
				t.Fatalf("settlement changed approval history: %#v", operation)
			}
			materialized, found, err := store.GetKernelToolResultMaterialization(context.Background(), operationID)
			if err != nil || !found {
				t.Fatalf("materialized=%#v found=%t err=%v", materialized, found, err)
			}
			var gotResult, wantResult map[string]any
			if err := json.Unmarshal(materialized.TerminalResultJSON, &gotResult); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.result), &wantResult); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotResult, wantResult) {
				t.Fatalf("materialized result=%#v want=%#v", gotResult, wantResult)
			}
			batch, found, err := store.GetToolCallBatch(context.Background(), stream.OwnerID, run.ToolBatchIDs[call.ID])
			if err != nil || !found || batch.State != workspace.ToolCallBatchStateSettled {
				t.Fatalf("batch=%#v found=%t err=%v", batch, found, err)
			}
			var executions int
			if err := db.QueryRow(`SELECT COUNT(*) FROM execution_log`).Scan(&executions); err != nil {
				t.Fatal(err)
			}
			if executions != 0 || server.activeKernelOperationCount() != 0 {
				t.Fatalf("executions=%d active operations=%d", executions, server.activeKernelOperationCount())
			}
		})
	}
}
