package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

// Prepare/Start use the existing durable operation owner with a genuinely new
// live claim. The immutable source admission remains older than that claim.
func specialistRecoveredDecisionOperation(t *testing.T, app *Server, stream transcriptstore.Stream, claim transcriptstore.RunnerClaim) (workspace.KernelLocalOperation, transcriptstore.RunnerClaim) {
	t.Helper()
	run := &sessionRunnerChatRun{SessionID: stream.SessionID, Attempt: int(claim.Attempt), ClaimToken: claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claim}}
	call := agentruntime.ToolCall{ID: "specialist-recovered-repl", Name: "repl", Arguments: json.RawMessage(`{"code":"import host"}`)}
	if err := app.checkpointChatModelToolCalls(SessionRunnerChatOptions{RunnerID: claim.RunnerID}, run, []agentruntime.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	store, repo := app.workspaceStore, app.transcriptStore
	operation, found, err := store.GetKernelLocalOperationByToolCall(t.Context(), stream.OwnerID, stream.UID, call.ID)
	if err != nil || !found {
		t.Fatalf("source operation found=%t err=%v", found, err)
	}
	operation, err = store.ResolveKernelLocalOperationApproval(t.Context(), workspace.ResolveKernelLocalOperationApprovalInput{
		OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "specialist-recovery-approved",
		Scope: "once", Source: "user", ActorID: stream.OwnerID, CurrentClaim: claim})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _, _, err := repo.PauseRunnerForApproval(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "specialist-recovery-waiting", Phase: transcriptstore.RunnerPhaseWaitingApproval,
		Resumable: true, PayloadJSON: []byte(`{"status":"awaiting_approval"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// This is the existing aggregate AskUser-response admission shape, not a
	// canonical new composer task that would supersede an unstarted operation.
	if err := repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		_, _, err := tx.AppendFrameInputResponse(t.Context(), transcriptstore.AppendFrameInputResponseInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, FrameID: stream.FrameID, ClientMessageID: "specialist-recovery-next-response",
			PayloadJSON: []byte(`{"role":"user","messageOrigin":"input_response","text":"synthetic answer","content":[{"type":"text","text":"synthetic answer"}]}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, RunnerID: "specialist-recovery-new-runner", TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("response recovery claim=%#v err=%v", claimed, err)
	}
	current := claimed.Claim
	if current.ClaimedInputRevision <= operation.AdmittedInputRevision {
		t.Fatal("fixture did not establish distinct source and current revisions")
	}
	operation, err = store.PrepareKernelLocalOperation(t.Context(), workspace.PrepareKernelLocalOperationInput{
		OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: current, BootID: "specialist-recovery-boot", KernelID: "specialist-recovery-kernel", KernelGeneration: 1,
		ConfinementSHA256: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	operation, err = store.StartKernelLocalOperation(t.Context(), workspace.StartKernelLocalOperationInput{
		OwnerUserID: stream.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		Claim: current, BootID: "specialist-recovery-boot", ExecutionID: "specialist-recovery-cell"})
	if err != nil {
		t.Fatal(err)
	}
	return operation, current
}
