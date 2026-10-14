package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

// This tests the shared Engine -> real Transcript batch -> real SQLite result
// store contract. The server projection/authority integration has its own test;
// the compacted messages here deliberately retain no old assistant calls.
func TestEngineFreshCallsAfterCompactionKeepSQLiteResultIdentity(t *testing.T) {
	store, repo, claim := newToolCallBatchFixture(t)
	ctx := context.Background()
	var records []RunnerLargeToolResult
	var inputs []WriteRunnerLargeToolResultInput
	var batches []ToolCallBatch
	for invocation := 0; invocation < 2; invocation++ {
		var batch ToolCallBatch
		var item ToolCallBatchItem
		var resultWriteErr error
		model := &identityScopeModel{call: agentruntime.ToolCall{
			ID: "call_0", Name: "read_file", Arguments: json.RawMessage(`{"path":"observation.json"}`),
		}}
		engine := agentruntime.Engine{
			Model:                  model,
			ToolCallIdentityPolicy: agentruntime.DurableToolCallIdentity,
			Tools: agentruntime.FuncToolGateway(func(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
				raw, _ := json.Marshal(map[string]any{"ok": true, "observation": invocation, "body": strings.Repeat("observed evidence ", 1500)})
				// Persisted consumers key a call by its canonical identity within its
				// stream. Deliberately do not include content or the source event in
				// this key: changed bytes for the SAME call must remain a conflict.
				digest := sha256.Sum256([]byte(claim.StreamUID + "\x00" + call.ID + "\x00" + call.Name))
				input := WriteRunnerLargeToolResultInput{
					ArtifactID: "large-tool-result-" + hex.EncodeToString(digest[:16]),
					ProjectID:  "tool-batch-project", RootFrameID: "tool-batch-frame", FrameID: "tool-batch-frame",
					StreamUID: claim.StreamUID, OwnerUserID: claim.OwnerID, RunnerID: claim.RunnerID,
					ClaimToken: claim.ClaimToken, Attempt: claim.Attempt, SourceEventID: item.StartedEventID,
					ToolCallID: call.ID, ToolName: call.Name, Content: raw,
				}
				record, err := store.WriteRunnerLargeToolResult(ctx, input)
				if err != nil {
					resultWriteErr = err
					return agentruntime.ToolResult{}, err
				}
				inputs = append(inputs, input)
				records = append(records, record)
				return agentruntime.ToolResult{Value: map[string]any{"ok": true}}, nil
			}),
			OnEventError: func(event agentruntime.Event) error {
				switch event.Type {
				case agentruntime.EventModelResponse:
					if len(event.ToolCalls) == 0 {
						return nil
					}
					call := event.ToolCalls[0]
					var arguments map[string]any
					if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
						return err
					}
					var items []ToolCallBatchItem
					batch, items = createToolCallBatchForTest(t, store, repo, claim, fmt.Sprintf("identity-proposal-%d", invocation),
						toolCallBatchTestCall{id: call.ID, name: call.Name, arguments: arguments})
					var err error
					batch, err = store.ClaimToolCallBatch(ctx, ClaimToolCallBatchInput{
						Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion,
					})
					item = items[0]
					return err
				case agentruntime.EventToolStarted:
					batch, item = appendToolCallBatchStart(t, store, repo, claim, batch, item, fmt.Sprintf("identity-start-%d", invocation))
				case agentruntime.EventToolCompleted:
					batch, item = appendToolCallBatchTerminal(t, store, repo, claim, batch, item,
						fmt.Sprintf("identity-complete-%d", invocation), ToolCallBatchItemStateCompleted, map[string]any{"ok": true}, "")
				}
				return nil
			},
		}
		result, err := engine.Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{
			{Role: "system", Content: "Compacted prior observations."}, {Role: "user", Content: "Read the next observation."},
		}, Tools: []agentruntime.ToolSchema{{Name: "read_file"}}})
		if err != nil || result.FinalMessage.Content != "done" || len(records) != invocation+1 {
			t.Fatalf("invocation %d: results=%d batch=%s source=%d err=%v result_write_error=%v; fresh call must not collide with a prior result",
				invocation, len(records), batch.BatchID, item.StartedEventID, err, resultWriteErr)
		}
		batches = append(batches, batch)
	}
	if records[0].ToolCallID == records[1].ToolCallID || records[0].SourceEventID == records[1].SourceEventID ||
		records[0].VersionID == records[1].VersionID || batches[0].BatchID == batches[1].BatchID {
		t.Fatalf("new model invocations aliased their durable identities: %#v", records)
	}
	for index, input := range inputs {
		replayed, err := store.WriteRunnerLargeToolResult(ctx, input)
		if err != nil || replayed.VersionID != records[index].VersionID || replayed.SourceEventID != records[index].SourceEventID {
			t.Fatalf("same-source replay %d: record=%#v err=%v", index, replayed, err)
		}
		input.Content = []byte(`{"ok":true,"body":"changed bytes"}`)
		if _, err := store.WriteRunnerLargeToolResult(ctx, input); !errors.Is(err, ErrRunnerLargeToolResultConflict) {
			t.Fatalf("same-source changed-byte conflict %d: %v", index, err)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runner_large_tool_results`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("durable result count=%d err=%v", count, err)
	}
}

type identityScopeModel struct {
	call agentruntime.ToolCall
	sent bool
}

func (model *identityScopeModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	message := agentruntime.Message{Role: "assistant", Content: "done"}
	if !model.sent {
		model.sent = true
		message.Content = ""
		message.ToolCalls = []agentruntime.ToolCall{model.call}
	}
	return agentruntime.ModelResponse{Message: message}, nil
}
