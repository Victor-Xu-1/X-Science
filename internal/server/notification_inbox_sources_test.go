package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestNotificationInboxRealApprovalIsReadOnlyAndContainsNoToolPayload(t *testing.T) {
	f := newNotificationInboxFixture(t)
	stream, claim := f.begin(t, "inbox-owner", "approval")
	// Match the real approval path: its distinct waiting-approval boundary
	// binds pending metadata in the checkpoint's existing transaction.
	_, _, _, err := f.repo.PauseRunnerForApproval(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "approval-wait", Phase: transcriptstore.RunnerPhaseWaitingApproval, Resumable: true,
		PayloadJSON: []byte(`{"status":"awaiting_approval","detail":"waiting for approval"}`), Destinations: []string{"ws"},
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, _ transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, err := f.store.BindAgentToolApprovalTransaction(ctx, tx, workspace.BindAgentToolApprovalInput{
				FrameID: stream.FrameID, ApprovalID: "actual-approval", ToolCallID: "actual-tool-call", ToolName: "local_exec",
				Description: "PRIVATE_TOOL_CODE_DO_NOT_PROJECT", Target: "PRIVATE_PATH_DO_NOT_PROJECT",
			})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readNotificationInbox(t, f, "inbox-owner")
	if len(snapshot.items) != 1 || snapshot.items[0]["kind"] != "authorization.required" || snapshot.items[0]["action_state"] != "pending" {
		t.Fatalf("canonical approval missing=%#v", snapshot)
	}
	raw, _ := json.Marshal(snapshot.items)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "local_exec") {
		t.Fatal("inbox projected approval execution input")
	}
	memoryCompatJSON(t, f.app.Handler(), http.MethodPost, notificationInboxTestPath+"/read-all", map[string]any{"read_receipt": snapshot.receipt}, "inbox-owner", http.StatusOK)
	metadata, found, err := f.store.GetFrameRuntimeMetadata(stream.FrameID)
	if err != nil || !found || len(compatibilityServerPendingInputs(metadata.ContextData)) != 1 {
		t.Fatal("human read resolved actual approval")
	}
}

func TestNotificationInboxRealFailureIsNotCompletionAndCancellationIsNotFailure(t *testing.T) {
	f := newNotificationInboxFixture(t)
	for _, status := range []string{"failed", "cancelled"} {
		_, claim := f.begin(t, "inbox-owner", status)
		payload, _ := json.Marshal(map[string]any{"status": status, "detail": "canonical terminal"})
		if _, _, _, err := f.repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
			Claim: claim, ClientMessageID: "finish-" + status, Status: status, PayloadJSON: payload,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := readNotificationInbox(t, f, "inbox-owner")
	if len(snapshot.items) != 1 || snapshot.items[0]["kind"] != "task.failed" || snapshot.items[0]["conversation_id"] != "failed" {
		t.Fatalf("terminal taxonomy drift=%#v", snapshot)
	}
}
