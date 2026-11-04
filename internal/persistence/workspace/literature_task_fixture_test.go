package workspace

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"testing"
	transcriptstore "x-science/internal/persistence/transcript"
)

func literatureTaskFixture(t *testing.T) (*Store, *transcriptstore.Repository, LiteratureTaskAuthority) {
	t.Helper()
	store, repo, stream, claim := newTranscriptCancellationFixture(t)
	access, found, err := store.GetKernelFrameAccessContext(t.Context(), stream.FrameID)
	if err != nil || !found {
		t.Fatalf("access=%#v err=%v", access, err)
	}
	const callID = "library-task-tool"
	payload, _ := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{
		map[string]any{"id": callID, "type": "function", "name": "save_to_inbox", "arguments": map[string]any{"references": []any{}}},
	}})
	var batch ToolCallBatch
	var items []ToolCallBatchItem
	_, _, _, err = repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "library-task-batch", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return transcriptstore.RunnerCheckpointCommitReceipt{ToolBatch: &transcriptstore.RunnerCheckpointToolBatchReceipt{BatchID: batch.BatchID, CallCount: batch.CallCount}}, err
		}})
	if err != nil || len(items) != 1 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	batch, err = store.ClaimToolCallBatch(t.Context(), ClaimToolCallBatchInput{Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	started, _ := json.Marshal(map[string]any{"status": "running", "toolCallId": callID, "toolName": "save_to_inbox", "toolPhase": "start"})
	_, _, _, err = repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "library-task-start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: started,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := store.StartToolCallBatchItemTx(ctx, tx, StartToolCallBatchItemInput{Claim: claim, BatchID: batch.BatchID, Ordinal: items[0].Ordinal,
				ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: items[0].StateVersion, StartedEvent: event})
			return transcriptstore.RunnerCheckpointCommitReceipt{}, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	return store, repo, LiteratureTaskAuthority{Access: access, Stream: stream, Claim: claim, ToolCallID: callID}
}

func literatureTaskRequest() LiteratureTaskInboxRequest {
	return LiteratureTaskInboxRequest{OperationID: uuid.NewString(), References: []LiteratureTaskReference{{
		Provider: "crossref", ExternalID: "10.1234/task-inbox", Title: "Synthetic task discovery",
		Metadata: map[string]any{"doi": "10.1234/task-inbox", "precise": json.Number("9007199254740993"), "project_id": "forged-project"},
	}}}
}

func literatureTaskCounts(t *testing.T, s *Store, want int) {
	t.Helper()
	for _, table := range []string{"literature_candidates", "literature_candidate_task_origins", "mutation_result_ledger"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count=%d want=%d", table, count, want)
		}
	}
}
