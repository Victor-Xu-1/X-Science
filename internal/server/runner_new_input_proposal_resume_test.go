package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRunnerNewComposerInputSettlesDeferredTailWithoutExecution(t *testing.T) {
	f := newCorrectionRouteFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := disableBundledMCPForLocalFixture(ctx, f.server, f.stream.OwnerID); err != nil {
		t.Fatal(err)
	}
	calls := []agentruntime.ToolCall{
		{ID: "old-compute-one", Name: "python", Arguments: json.RawMessage(`{"code":"print(1)","environment":"science"}`)},
		{ID: "old-compute-two", Name: "bash", Arguments: json.RawMessage(`{"command":"true","environment":"science","human_description":"Check runtime"}`)},
	}
	options := SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}
	if err := f.server.checkpointChatModelToolCalls(options, f.run, calls); err != nil {
		t.Fatal(err)
	}
	batchID := f.run.ToolBatchIDs[calls[0].ID]
	if batchID == "" || batchID != f.run.ToolBatchIDs[calls[1].ID] {
		t.Fatal("fixture did not persist the original ordered proposal")
	}
	if _, _, err := f.store.CancelFrameWithTranscript(ctx, f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	_, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "changed-composer-input",
		FrameEventID: "changed-composer-event", MessageUUID: "changed-composer-message", MessageOrigin: "input_response",
		Text:      "Use the updated execution constraints before continuing.",
		InputData: map[string]any{"request": "Use the updated execution constraints before continuing."},
	})
	if err != nil || !created {
		t.Fatalf("composer input created=%t err=%v", created, err)
	}
	if _, err := f.store.CreateAutoResumeDispatch(f.stream.FrameID, f.stream.RootFrameID, f.stream.ProjectID, "OPERON", "new_composer_input"); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("changed-composer-dispatch", time.Minute)
	if err != nil || !found {
		t.Fatalf("dispatch found=%t err=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID,
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceUserInput,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	stream, err := f.repo.ValidateLiveRunnerClaim(ctx, claimed.Claim)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{
		SessionID: stream.SessionID, Attempt: int(claimed.Claim.Attempt), ClaimToken: claimed.Claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim},
	}
	options.RunnerID = claimed.Claim.RunnerID
	// Enter the real recovery boundary, which first claims/binds each durable
	// batch to this new runner before settling its pending tail.
	resumed, err := f.server.resumeApprovedAgentKernelOperations(ctx, options, run)
	if err != nil || !resumed {
		t.Fatalf("pending proposal recovery resumed=%t err=%v", resumed, err)
	}
	batch, found, err := f.store.GetToolCallBatch(ctx, stream.OwnerID, batchID)
	if err != nil || !found || batch.State != workspace.ToolCallBatchStateSettled || batch.NextOrdinal != 2 {
		t.Fatalf("protocol tail was not settled: %+v err=%v", batch, err)
	}
	items, err := f.store.ListToolCallBatchItems(ctx, stream.OwnerID, batchID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	for i, item := range items {
		if item.ToolCallID != calls[i].ID || item.StartedEventID != 0 || item.WaitingEventID != 0 || item.TerminalEventID <= 0 {
			t.Fatalf("recovery invented execution or changed identity: %+v", item)
		}
		if _, exists, err := f.store.GetKernelLocalOperationByToolCall(ctx, stream.OwnerID, stream.UID, item.ToolCallID); err != nil || exists {
			t.Fatalf("obsolete proposal admitted execution: exists=%t err=%v", exists, err)
		}
	}
	entries, err := f.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := sessionEntriesToProviderMessages("system", entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		count := 0
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == call.ID && strings.Contains(message.Content, "superseded_by_new_user_input") && strings.Contains(message.Content, `"executed":false`) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("original call %s has %d nonexecution results", call.ID, count)
		}
	}
	if repeated, err := f.server.resumeApprovedAgentKernelOperations(ctx, options, run); err != nil || repeated {
		t.Fatalf("settled tail was replayed again: repeated=%t err=%v", repeated, err)
	}
	if logs, err := f.store.ListExecutionLog(stream.FrameID, ""); err != nil || len(logs) != 0 {
		t.Fatalf("nonexecution recovery created execution logs: count=%d err=%v", len(logs), err)
	}
}
