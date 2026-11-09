package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

func TestRejectedArgumentsCheckpointReopensWithoutExecution(t *testing.T) {
	for _, raw := range []string{`[]`, `{"command":`, `{"command":"first","command":"second"}`} {
		t.Run(raw, func(t *testing.T) {
			fixture := newCorrectionRouteFixture(t)
			ctx := context.Background()
			call := agentruntime.ToolCall{
				ID: "rejected-arguments", Name: "bash", Arguments: json.RawMessage(raw),
				RejectedBeforeExecution: true,
				PreExecutionRejection:   json.RawMessage(`{"ok":false,"executed":false,"preflight":true,"code":"invalid_tool_arguments","message":"Arguments require one unambiguous JSON object.","retryable":true}`),
			}
			options := SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}
			if err := fixture.server.checkpointChatModelToolCalls(options, fixture.run, []agentruntime.ToolCall{call}); err != nil {
				t.Fatalf("a rejected proposal must remain durable without execution: %v", err)
			}
			batchID := fixture.run.ToolBatchIDs[call.ID]
			items, err := fixture.store.ListToolCallBatchItems(ctx, fixture.stream.OwnerID, batchID)
			if err != nil || len(items) != 1 || string(items[0].ArgumentsJSON) != "{}" {
				t.Fatalf("rejected arguments were not kept inert: items=%#v err=%v", items, err)
			}
			batch, found, err := fixture.store.GetToolCallBatch(ctx, fixture.stream.OwnerID, batchID)
			if err != nil || !found {
				t.Fatalf("canonical source batch unavailable: found=%t err=%v", found, err)
			}
			var checkpoint []byte
			if err := fixture.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, fixture.stream.UID, batch.SourceEventID).Scan(&checkpoint); err != nil {
				t.Fatal(err)
			}
			var stored struct {
				Calls []struct {
					Rejected      bool   `json:"rejectedBeforeExecution"`
					OriginalSHA   string `json:"originalArgumentsSHA256"`
					OriginalBytes int    `json:"originalArgumentsByteLength"`
				} `json:"modelToolCalls"`
			}
			if err := json.Unmarshal(checkpoint, &stored); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(call.Arguments)
			if len(stored.Calls) != 1 || !stored.Calls[0].Rejected || stored.Calls[0].OriginalSHA != hex.EncodeToString(digest[:]) || stored.Calls[0].OriginalBytes != len(raw) {
				t.Fatal("rejection lost its original non-executable proposal identity")
			}
			if err := fixture.server.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := workspace.Open(fixture.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			repo, err := reopened.TranscriptRepository(ctx)
			if err != nil {
				t.Fatal(err)
			}
			replay := New(Options{Workspace: reopened, Transcript: repo, FileRoot: t.TempDir()})
			t.Cleanup(func() { _ = replay.Close(context.Background()) })
			run := &sessionRunnerChatRun{SessionID: fixture.run.SessionID, Attempt: fixture.run.Attempt, ClaimToken: fixture.run.ClaimToken, Transcript: fixture.run.Transcript, AfterEventID: fixture.run.AfterEventID}
			if resumed, err := replay.resumePendingAgentToolCalls(ctx, options, run); err != nil || !resumed {
				t.Fatalf("restart did not settle the original rejection: resumed=%t err=%v", resumed, err)
			}
			boundary := run.AfterEventID
			if resumed, err := replay.resumePendingAgentToolCalls(ctx, options, run); err != nil || resumed || run.AfterEventID != boundary {
				t.Fatalf("second resume repeated the rejection: resumed=%t err=%v", resumed, err)
			}
			if _, found, err := reopened.GetKernelLocalOperationByToolCall(ctx, fixture.stream.OwnerID, fixture.stream.UID, call.ID); err != nil || found {
				t.Fatalf("rejected proposal obtained execution authority: found=%t err=%v", found, err)
			}
			items, err = reopened.ListToolCallBatchItems(ctx, fixture.stream.OwnerID, batchID)
			if err != nil || len(items) != 1 || items[0].StartedEventID != 0 || items[0].WaitingEventID != 0 || items[0].State != workspace.ToolCallBatchItemStateFailed {
				t.Fatalf("rejected proposal started or waited: items=%#v err=%v", items, err)
			}
			entries, err := replay.loadTranscriptRunnerReplay(ctx, run.Transcript, 100, 100)
			if err != nil {
				t.Fatal(err)
			}
			messages, err := sessionEntriesToProviderMessages("system", entries)
			if err != nil {
				t.Fatal(err)
			}
			feedback := 0
			for _, message := range messages {
				if message.Role == "tool" && message.ToolCallID == call.ID && strings.Contains(message.Content, `"executed":false`) && strings.Contains(message.Content, "invalid_tool_arguments") {
					feedback++
				}
			}
			if feedback != 1 {
				t.Fatalf("replay must keep one original rejection, got %d", feedback)
			}
		})
	}
}
