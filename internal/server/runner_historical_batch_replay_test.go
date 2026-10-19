package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestRunnerHistoricalBatchReplayPairsAfterSameBranchContinuation(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken,
		Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	options := SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}
	calls := []agentruntime.ToolCall{
		{ID: "retained-call-a", Name: "read_file", Arguments: json.RawMessage(`{"path":"notes-a.txt"}`)},
		{ID: "retained-call-b", Name: "read_file", Arguments: json.RawMessage(`{"path":"notes-b.txt"}`)},
	}
	if err := f.server.checkpointChatModelToolCalls(options, run, calls); err != nil {
		t.Fatal(err)
	}
	for ordinal, call := range calls {
		for _, event := range []agentruntime.Event{
			{Type: agentruntime.EventToolStarted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments)},
			{Type: agentruntime.EventToolCompleted, ToolName: call.Name, ToolCallID: call.ID, Arguments: string(call.Arguments),
				Result: fmt.Sprintf(`{"ok":true,"ordinal":%d}`, ordinal)},
		} {
			if err := f.server.checkpointSessionRunnerToolEvent(ctx, options, run, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	batchID := run.ToolBatchIDs[calls[0].ID]
	before, found, err := f.store.GetToolCallBatch(ctx, f.stream.OwnerID, batchID)
	if err != nil || !found || before.State != "settled" {
		t.Fatalf("real checkpoint producer did not settle the original batch: found=%t batch=%+v err=%v", found, before, err)
	}
	branch, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	mutation := "historical-next-input"
	messageID := "branch-continue:" + mutation
	continued, err := f.repo.AppendFrameUserEventToBranch(ctx, transcriptstore.AppendFrameUserEventToBranchInput{
		AppendFrameUserEventInput: transcriptstore.AppendFrameUserEventInput{
			StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: messageID, MessageUUID: messageID,
			FrameEventID: "frame-branch-continue:" + f.stream.FrameID + ":" + branch.ActiveBranchID + ":" + mutation,
			Text:         "Continue the generic verification.", Destinations: []string{"ws"},
		},
		TargetBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID,
		ExpectedGeneration: branch.Generation, ClientMutationID: mutation,
	})
	if err != nil || continued.Switched || continued.BranchGeneration != branch.Generation+1 {
		t.Fatalf("same-branch continuation=%+v err=%v", continued, err)
	}
	if _, err := f.store.CreateAutoResumeDispatch(f.stream.FrameID, f.stream.RootFrameID, f.stream.ProjectID,
		f.identity.access.Frame.AgentName, "new_composer_input"); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := f.store.ClaimNextCompatibilityFrameResumeDispatch("historical-next-dispatch", time.Minute)
	if err != nil || !found {
		t.Fatalf("new input dispatch found=%t err=%v", found, err)
	}
	claimed, err := f.repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceUserInput,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("new input claim=%+v err=%v", claimed, err)
	}
	stream, err := f.repo.ValidateLiveRunnerClaim(ctx, claimed.Claim)
	if err != nil {
		t.Fatal(err)
	}
	authority := &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}
	entries, err := f.server.loadTranscriptRunnerReplay(ctx, authority, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := sessionEntriesToProviderMessages("system", entries)
	if err != nil {
		t.Fatalf("same-branch retained transaction broke provider preparation: %v", err)
	}
	for _, call := range calls {
		roots, results := 0, 0
		for _, message := range messages {
			for _, replayed := range message.ToolCalls {
				if replayed.ID == call.ID {
					roots++
				}
			}
			if message.Role == "tool" && message.ToolCallID == call.ID {
				results++
			}
		}
		if roots != 1 || results != 1 {
			t.Fatalf("historical call %s replayed roots=%d results=%d", call.ID, roots, results)
		}
	}
	if _, err := sessionRunnerProviderContextTokenEstimate("system", entries); err != nil {
		t.Fatalf("preparation token estimate still rejects the complete history: %v", err)
	}
	runnable, err := f.store.ListRunnableToolCallBatches(ctx, claimed.Claim, 10)
	if err != nil || len(runnable) != 0 {
		t.Fatalf("historical read relation became executable: batches=%+v err=%v", runnable, err)
	}
	if _, err := f.store.ClaimToolCallBatch(ctx, workspace.ClaimToolCallBatchInput{
		Claim: claimed.Claim, BatchID: batchID, ExpectedStateVersion: before.StateVersion,
	}); !errors.Is(err, workspace.ErrToolCallBatchConflict) {
		t.Fatalf("historical generation acquired a new execution claim: %v", err)
	}
	after, found, err := f.store.GetToolCallBatch(ctx, f.stream.OwnerID, batchID)
	if err != nil || !found || after.BranchGeneration != before.BranchGeneration || after.State != before.State || after.StateVersion != before.StateVersion {
		t.Fatalf("read-only replay mutated the historical batch: before=%+v after=%+v err=%v", before, after, err)
	}
}
