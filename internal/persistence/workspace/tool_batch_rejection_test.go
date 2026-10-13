package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func createRejectedToolBatchFixture(t *testing.T, rejected bool, feedback bool) (*Store, *transcriptstore.Repository, transcriptstore.RunnerClaim, ToolCallBatch) {
	t.Helper()
	store, repo, claim := newToolCallBatchFixture(t)
	call := map[string]any{
		"id": "rejected", "type": "function", "name": "repl",
		"arguments":               map[string]any{"code": "print(1)", "human_description": "Read-only computation"},
		"rejectedBeforeExecution": rejected,
	}
	if !rejected {
		call["name"] = "read_file"
		call["arguments"] = map[string]any{"path": "notes.txt", "rejectedBeforeExecution": true}
	}
	if feedback {
		call["preExecutionRejection"] = map[string]any{"ok": false, "executed": false, "code": "runtime_preflight_required", "message": "Current route unavailable"}
	}
	payload, err := json.Marshal(map[string]any{"status": "running", "modelToolCalls": []any{call}})
	if err != nil {
		t.Fatal(err)
	}
	var batch ToolCallBatch
	_, _, _, err = repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "rejection-source", Phase: transcriptstore.RunnerPhaseExecuting,
		Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var createErr error
			batch, _, _, createErr = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			return toolCallBatchTestCommitReceipt(batch, createErr)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, repo, claim, batch
}

func TestToolCallRejectionReadsOnlyCanonicalUnstartedAdmission(t *testing.T) {
	for _, feedback := range []bool{false, true} {
		store, repo, claim, batch := createRejectedToolBatchFixture(t, true, feedback)
		rejection, err := store.ReadPendingToolCallRejection(context.Background(), claim, batch.BatchID, 0)
		if err != nil || !rejection.Rejected || rejection.ToolCallID != "rejected" ||
			(len(rejection.FeedbackJSON) > 0) != feedback {
			t.Fatalf("rejection=%#v err=%v", rejection, err)
		}
		for _, mutate := range []func(*transcriptstore.RunnerClaim){
			func(c *transcriptstore.RunnerClaim) { c.OwnerID = "other-owner" },
			func(c *transcriptstore.RunnerClaim) { c.Attempt++ },
			func(c *transcriptstore.RunnerClaim) { c.ClaimedInputRevision++ },
		} {
			foreign := claim
			mutate(&foreign)
			if _, err := store.ReadPendingToolCallRejection(context.Background(), foreign, batch.BatchID, 0); err == nil {
				t.Fatal("foreign/stale claim read trusted rejection")
			}
		}
		items, err := store.ListToolCallBatchItems(context.Background(), claim.OwnerID, batch.BatchID)
		if err != nil {
			t.Fatal(err)
		}
		batch, err = store.ClaimToolCallBatch(context.Background(), ClaimToolCallBatchInput{Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
		if err != nil {
			t.Fatal(err)
		}
		appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "unexpected-start")
		if _, err := store.ReadPendingToolCallRejection(context.Background(), claim, batch.BatchID, 0); !errors.Is(err, ErrToolCallBatchConflict) {
			t.Fatalf("started rejection did not fail closed: %v", err)
		}
	}
}

func TestToolCallRejectionLegacyTerminalReplayPreservesUnknownLedger(t *testing.T) {
	for _, kind := range []string{"rejected", "not-rejected", "started", "waiting", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			store, repo, claim, batch := createRejectedToolBatchFixture(t, kind != "not-rejected", false)
			if kind == "started" || kind == "waiting" {
				items, err := store.ListToolCallBatchItems(context.Background(), claim.OwnerID, batch.BatchID)
				if err != nil {
					t.Fatal(err)
				}
				batch, err = store.ClaimToolCallBatch(context.Background(), ClaimToolCallBatchInput{Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
				if err != nil {
					t.Fatal(err)
				}
				batch, items[0] = appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "legacy-start")
				if kind == "waiting" {
					appendToolCallBatchWait(t, store, repo, claim, batch, items[0], "legacy-wait")
				}
			}
			if kind == "receipt" {
				_, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
					Claim: claim, ClientMessageID: "effect-receipt", Phase: transcriptstore.RunnerPhaseExecuting,
					Resumable: true, PayloadJSON: []byte(`{"status":"running","toolCallId":"rejected","toolPhase":"completed","toolResult":{"ok":true}}`),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := repo.FinishRunner(context.Background(), transcriptstore.FinishRunnerInput{
				Claim: claim, ClientMessageID: "legacy-failure", Status: "failed", PayloadJSON: []byte(`{"status":"failed"}`),
			}); err != nil {
				t.Fatal(err)
			}
			if recovered, err := store.ReconcileTerminalFrameToolCallBatch(context.Background(), claim.OwnerID, batch.BatchID); err != nil || !recovered {
				t.Fatalf("reconcile=%t err=%v", recovered, err)
			}
			items, err := store.ListToolCallBatchItems(context.Background(), claim.OwnerID, batch.BatchID)
			if err != nil {
				t.Fatal(err)
			}
			terminal := items[0].TerminalEventID
			rejection, err := store.ReadTerminalToolCallRejection(context.Background(), claim, terminal)
			if err != nil || rejection.Rejected != (kind == "rejected") {
				t.Fatalf("rejection=%#v err=%v", rejection, err)
			}
			foreign := claim
			foreign.OwnerID = "other-owner"
			if rejection, err := store.ReadTerminalToolCallRejection(context.Background(), foreign, terminal); err != nil || rejection.Rejected {
				t.Fatalf("foreign owner projected rejection=%#v err=%v", rejection, err)
			}
			stored, found, err := store.GetToolCallBatch(context.Background(), claim.OwnerID, batch.BatchID)
			if err != nil || !found || stored.State != ToolCallBatchStateOutcomeUnknown ||
				items[0].State != ToolCallBatchItemStateOutcomeUnknown {
				t.Fatalf("read-only replay altered unknown ledger: %#v err=%v", stored, err)
			}
		})
	}
}

func TestToolCallRejectionRejectsChangedCanonicalCoordinates(t *testing.T) {
	store, _, claim, batch := createRejectedToolBatchFixture(t, true, true)
	items, err := store.ListToolCallBatchItems(context.Background(), claim.OwnerID, batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ToolCallBatchItem){
		func(item *ToolCallBatchItem) { item.ToolCallID = "another-call" },
		func(item *ToolCallBatchItem) { item.ToolName = "python" },
		func(item *ToolCallBatchItem) { item.ArgumentsJSON = []byte(`{"code":"changed"}`) },
		func(item *ToolCallBatchItem) { item.ArgumentsSHA256 = "changed" },
		func(item *ToolCallBatchItem) { item.Ordinal++ },
	} {
		changed := items[0]
		mutate(&changed)
		if _, err := readCanonicalToolCallRejection(context.Background(), store.db, batch, changed); !errors.Is(err, ErrToolCallBatchConflict) {
			t.Fatalf("changed coordinates accepted: %#v err=%v", changed, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ReadPendingToolCallRejection(cancelled, claim, batch.BatchID, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claim accepted: %v", err)
	}
}
