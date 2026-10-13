package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestKernelAdmissionPostReconcilePreservesDurableTerminalOutcome(t *testing.T) {
	for _, test := range []struct {
		name      string
		started   bool
		preflight bool
	}{
		{"competing-admission-preflight", false, true},
		{"completed-execution", true, false},
		{"worker-preflight-after-start", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, repo, db := newTranscriptWebFixture(t)
			seedTranscriptWebFrame(t, store, "local", "project-terminal-race", "frame-terminal-race")
			server := New(Options{Workspace: store, Transcript: repo, FileRoot: t.TempDir()})
			t.Cleanup(func() { _ = server.Close(context.Background()) })
			if _, _, err := server.submitFrameMessage(store, frameMessageSubmission{
				FrameID: "frame-terminal-race", MessageUUID: "terminal-race-message",
				ClientMessageID: "terminal-race-client", Text: "execute the validated input",
			}); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			stream, found, err := repo.GetFrameStreamBySession(ctx, "local", "frame-terminal-race")
			if err != nil || !found {
				t.Fatalf("stream found=%t err=%v", found, err)
			}
			claimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "terminal-race-runner",
				TTL: 5 * time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
			})
			if err != nil || !claimed.Claimed {
				t.Fatalf("claim=%#v err=%v", claimed, err)
			}
			run := &sessionRunnerChatRun{
				SessionID: stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken,
				Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim},
			}
			call := agentruntime.ToolCall{ID: "terminal-race-call", Name: "python", Arguments: json.RawMessage(`{"code":"print(1)","environment":"python"}`)}
			if err := server.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: claimed.Claim.RunnerID}, run, []agentruntime.ToolCall{call}); err != nil {
				t.Fatal(err)
			}
			operation, found, err := store.GetKernelLocalOperation(ctx, stream.OwnerID, run.KernelOperationIDs[call.ID])
			if err != nil || !found {
				t.Fatalf("operation found=%t err=%v", found, err)
			}
			operation, err = store.ResolveKernelLocalOperationApproval(ctx, workspace.ResolveKernelLocalOperationApprovalInput{
				OwnerUserID: stream.OwnerID, OperationID: operation.OperationID,
				ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID,
				Approved: true, DecisionID: "terminal-race-decision", Scope: "once",
				Source: "policy", ActorID: "system", CurrentClaim: claimed.Claim,
			})
			if err != nil || !kernelAdmissionFailureIsUnstarted(operation) {
				t.Fatalf("initial approved operation=%#v err=%v", operation, err)
			}
			original := map[string]any{"ok": false, "error": "stale admission error"}
			proposed := map[string]any{"ok": false, "executed": false, "preflight": true, "code": "execution_preflight_failed", "message": "stale proposed outcome"}
			actual := map[string]any{"ok": !test.preflight, "executed": !test.preflight, "message": "durable competing outcome"}
			if test.preflight {
				actual["preflight"], actual["code"] = true, "input_authority_required"
			}
			if test.started {
				operation, err = store.PrepareKernelLocalOperation(ctx, workspace.PrepareKernelLocalOperationInput{
					OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					Claim: claimed.Claim, BootID: "terminal-race-boot", KernelID: "terminal-race-kernel",
					KernelGeneration: 1, ConfinementSHA256: strings.Repeat("c", 64),
				})
				if err != nil {
					t.Fatal(err)
				}
				operation, err = store.StartKernelLocalOperation(ctx, workspace.StartKernelLocalOperationInput{
					OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					Claim: claimed.Claim, BootID: "terminal-race-boot", ExecutionID: "terminal-race-execution",
				})
				if err != nil {
					t.Fatal(err)
				}
				terminalState := workspace.KernelLocalOperationStateCompleted
				if test.preflight {
					terminalState = workspace.KernelLocalOperationStateCancelled
				}
				actualJSON, _ := json.Marshal(actual)
				_, err = store.FinishKernelLocalOperation(ctx, workspace.FinishKernelLocalOperationInput{
					OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					Claim: claimed.Claim, BootID: "terminal-race-boot", ExecutionID: operation.ExecutionID,
					TerminalState: terminalState, ReasonCode: "execution_settled", TerminalResultJSON: actualJSON,
					ExecutionLog: workspace.SaveExecutionLogInput{
						Record: workspace.ExecutionLogRecord{
							ID: operation.ExecutionID, FrameID: operation.FrameID, KernelID: operation.KernelID,
							CondaEnv: operation.Environment, Language: "python", Source: "print(1)",
							ExitStatus: "ok", Origin: "agent", ExecutedAt: time.Now().UTC(),
						},
						ExpectedOwnerID: stream.OwnerID, ExpectedProjectID: operation.ProjectID,
						ExpectedFrameIncarnationID: operation.FrameIncarnationID, ExpectedRootFrameIncarnationID: operation.RootFrameIncarnationID,
					},
				})
			} else {
				_, _, err = server.reconcileKernelOperationPreflightResult(ctx, run, call.ID, actual)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Deterministically interleave a competing settlement after the initial
			// approved read and before reconciliation, without a production hook.
			settled, handled, err := server.reconcileKernelOperationPreflightResult(ctx, run, call.ID, proposed)
			if err != nil || !handled {
				t.Fatalf("reconciliation handled=%t err=%v", handled, err)
			}
			projected, preflight, err := server.settledKernelAdmissionFailureResult(settled, original)
			want := original
			if test.preflight {
				want = actual
			}
			if err != nil || preflight != test.preflight || !reflect.DeepEqual(projected, want) {
				t.Fatalf("projected=%#v preflight=%t err=%v want=%#v", projected, preflight, err, want)
			}
			// Replayed failure follows the same durable authority, including a
			// genuine worker preflight with prepared/start records and execution ID.
			duplicate, preflight, err := server.settleKernelAdmissionFailure(run, call.ID, original)
			if err != nil || preflight != test.preflight || !reflect.DeepEqual(duplicate, want) {
				t.Fatalf("duplicate=%#v preflight=%t err=%v", duplicate, preflight, err)
			}
			persisted, err := server.kernelLocalOperationPersistedResult(settled)
			if err != nil || !reflect.DeepEqual(persisted, actual) {
				t.Fatalf("durable result changed: %#v err=%v", persisted, err)
			}
			var executions int
			if err := db.QueryRow(`SELECT COUNT(*) FROM execution_log`).Scan(&executions); err != nil {
				t.Fatal(err)
			}
			if executions != map[bool]int{false: 0, true: 1}[test.started] {
				t.Fatalf("execution record changed: %d", executions)
			}
		})
	}
}
