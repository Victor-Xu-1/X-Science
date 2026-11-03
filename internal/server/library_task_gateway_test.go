package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestLibraryTaskToolAndSDKUseTheSameReceiptAuthority(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	const callID = "task-library-direct"
	request := map[string]any{"operation_id": "df35bc1a-eb24-4dce-a022-5660e51535be", "references": []any{
		map[string]any{"provider": "crossref", "external_id": "10.1234/shared-inbox", "title": "Shared Inbox authority", "metadata": map[string]any{"doi": "10.1234/shared-inbox", "project_id": "forged", "requiresUserInput": true}},
	}}
	payload, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{map[string]any{"id": callID, "name": librarySaveToInboxToolName, "type": "function", "arguments": request}}})
	var batch workspace.ToolCallBatch
	var items []workspace.ToolCallBatchItem
	_, _, _, err := f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim, ClientMessageID: "library-model-proposal", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = f.store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}, err
		}})
	if err != nil || len(items) != 1 {
		t.Fatalf("batch=%#v %v", batch, err)
	}
	batch, err = f.store.ClaimToolCallBatch(t.Context(), workspace.ClaimToolCallBatchInput{Claim: f.claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	started, _ := json.Marshal(map[string]any{"status": "running", "toolCallId": callID, "toolName": librarySaveToInboxToolName, "toolPhase": "start", "toolInput": request})
	_, _, _, err = f.repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim, ClientMessageID: "library-tool-start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: started,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := f.store.StartToolCallBatchItemTx(ctx, tx, workspace.StartToolCallBatchItemInput{Claim: f.claim, BatchID: batch.BatchID, Ordinal: items[0].Ordinal,
				ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: items[0].StateVersion, StartedEvent: event})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withTranscriptRunnerChatRun(t.Context(), &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}})
	schemas := []agentruntime.ToolSchema{librarySaveToInboxSchema()}
	gateway := serverAgentRuntimeToolGateway{server: f.server, libraryAccess: f.server.captureLibraryTaskAccess(ctx, f.stream.FrameID), sessionID: f.stream.FrameID,
		allowedTools: []string{librarySaveToInboxToolName}, toolSchemas: schemas,
		toolValidators: agentRuntimeToolValidators(schemas), hasToolSnapshot: true}
	arguments, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Execute(ctx, agentruntime.ToolCall{ID: callID, Name: librarySaveToInboxToolName, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	first := result.Value
	status, message := agentRuntimeToolResponseStatus(first)
	if status != "completed" || message != "" || result.PendingWorkWait || result.Terminal {
		t.Fatalf("Inbox review became a task control state: status=%q message=%q result=%#v", status, message, result)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if agentruntime.ClassifyToolResult(first) != agentruntime.ToolResultSucceeded || agentruntime.ClassifyToolResult(envelope) != agentruntime.ToolResultSucceeded {
		t.Fatal("nested pending candidate or untrusted metadata changed typed/JSON execution classification")
	}
	for _, key := range []string{"requiresUserInput", "requires_user_input", "requiresApproval", "approval_request_id"} {
		if _, exists := envelope[key]; exists {
			t.Fatalf("Library result exposed task approval control: %s", raw)
		}
	}
	// An SDK retry can resolve the original accepted logical operation; it does
	// not need a new writable cell or repeat the project association.
	bound := kernelHostExecutionIdentity{access: f.identity.access, transcriptExecution: kernelTranscriptExecutionBinding{
		stream: f.stream, claim: f.claim, outerToolUseID: callID, kernelID: "library-kernel", kernelGeneration: 1, currentKernelGeneration: func() uint64 { return 2 }}}
	replay, err := f.server.handleKernelLibraryInbox(ctx, bound, f.identity.access, []any{request}, nil)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("adapter replay=%#v first=%#v err=%v", replay, first, err)
	}
	candidates, err := f.store.ListLiteratureCandidates(t.Context(), f.identity.access.UserID, "pending", "")
	if err != nil || len(candidates.Candidates) != 1 || candidates.Candidates[0].TaskOrigins[0].ProjectID != f.identity.access.Frame.ProjectID {
		t.Fatalf("candidates=%#v %v", candidates, err)
	}
}

func TestLibraryTaskGatewayRejectsAuthorityOverridesAndOversizedBatch(t *testing.T) {
	f := newKernelSkillDraftFixture(t)
	startDelegateInitializationBatch(t, f.agentSaveArtifactsFixture, f.bound.transcriptExecution.outerToolUseID)
	for _, mode := range []string{"owner_override", "origin_override", "too_many", "invalid_uuid"} {
		t.Run(mode, func(t *testing.T) {
			ref := map[string]any{"provider": "crossref", "external_id": "source", "title": "Synthetic", "metadata": map[string]any{}}
			request := map[string]any{"operation_id": "6fc6e9d8-2814-4be1-9d0c-e26680bc8cc6", "references": []any{ref}}
			switch mode {
			case "owner_override":
				request["owner_id"] = "foreign"
			case "origin_override":
				ref["task_origins"] = []any{}
			case "too_many":
				request["references"] = []any{ref, ref, ref, ref, ref, ref, ref, ref, ref, ref, ref}
			case "invalid_uuid":
				request["operation_id"] = "not-uuid"
			}
			if result, err := f.call("host.library.save_to_inbox", request); err == nil {
				t.Fatalf("forged request accepted: %#v", result)
			}
		})
	}
	pending, err := f.store.ListLiteratureCandidates(t.Context(), f.identity.access.UserID, "pending", "")
	if err != nil || len(pending.Candidates) != 0 {
		t.Fatalf("invalid batch wrote candidate: %#v %v", pending, err)
	}
}
