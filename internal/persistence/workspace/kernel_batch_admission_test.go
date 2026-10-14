package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestKernelBatchAdmissionOrdersCanonicalInputAndSurvivesReopen(t *testing.T) {
	for _, tool := range []string{"python", "r", "bash"} {
		t.Run(tool, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "workspace.db")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			store, repo, claim := initializeKernelLocalOperationFixture(t, store)
			t.Cleanup(func() { _ = store.Close() })
			batch, items := createOrderedKernelBatch(t, store, repo, claim, tool)
			if _, err := store.KernelToolCallBatchAdmission(ctx, claim, batch.BatchID, 1); err == nil {
				t.Fatal("later ordinal admitted before setup receipt")
			}
			batch, items[0] = appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "setup-start")
			batch, items[0] = appendToolCallBatchTerminal(t, store, repo, claim, batch, items[0], "setup-complete", ToolCallBatchItemStateCompleted, map[string]any{"environment": map[string]any{"name": "verified-env", "status": "ready"}}, "")
			before, err := store.KernelToolCallBatchAdmission(ctx, claim, batch.BatchID, 1)
			if err != nil || !before.Deferred || before.OperationFound {
				t.Fatalf("before=%#v err=%v", before, err)
			}
			proposal := append([]byte(nil), items[1].ArgumentsJSON...)
			admission := orderedKernelAdmissionInput(t, batch, items[1])
			operation, err := appendOrderedKernelStart(store, repo, claim, batch, items[1], admission, nil)
			if err != nil {
				t.Fatal(err)
			}
			if operation.SourceEventID != batch.SourceEventID || operation.ToolCallOrdinal != 1 || operation.Environment != "verified-env" || operation.State != KernelLocalOperationStatePendingApproval {
				t.Fatalf("operation=%#v", operation)
			}
			if bytes.Equal(operation.InputJSON, proposal) {
				t.Fatal("unresolved proposal was admitted instead of canonical input")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			repo, err = store.TranscriptRepository(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state, err := store.KernelToolCallBatchAdmission(ctx, claim, batch.BatchID, 1)
			if err != nil || !state.OperationFound || state.Operation.OperationID != operation.OperationID || !bytes.Equal(state.Item.ArgumentsJSON, proposal) {
				t.Fatalf("state=%#v err=%v", state, err)
			}
			current, _, err := store.GetToolCallBatch(ctx, claim.OwnerID, batch.BatchID)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := appendOrderedKernelStart(store, repo, claim, current, state.Item, admission, nil)
			if err != nil || replayed.OperationID != operation.OperationID || replayed.ApprovalRequestID != operation.ApprovalRequestID {
				t.Fatalf("replay=%#v err=%v", replayed, err)
			}
			approved, err := store.ResolveKernelLocalOperationApproval(ctx, ResolveKernelLocalOperationApprovalInput{OwnerUserID: claim.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "approve-ordered", Scope: "once", Source: "user", ActorID: claim.OwnerID, CurrentClaim: claim})
			if err != nil {
				t.Fatal(err)
			}
			runnable, err := store.ListRunnableKernelLocalOperations(ctx, claim, 10)
			if err != nil || len(runnable) != 1 || runnable[0].OperationID != approved.OperationID {
				t.Fatalf("runnable=%#v err=%v", runnable, err)
			}
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operations WHERE stream_uid=?`, claim.StreamUID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("count=%d err=%v", count, err)
			}
		})
	}
}

func TestKernelBatchAdmissionRollbackAndAuthority(t *testing.T) {
	for _, scenario := range []string{"hook-failure", "wrong-proposal", "wrong-ordinal", "wrong-owner", "wrong-batch", "wrong-input-hash", "wrong-input-size", "missing-receipt", "missing-hook"} {
		t.Run(scenario, func(t *testing.T) {
			store, repo, claim := newKernelLocalOperationFixture(t)
			batch, items := createOrderedKernelBatch(t, store, repo, claim, "python")
			batch, items[0] = appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "setup-start")
			batch, items[0] = appendToolCallBatchTerminal(t, store, repo, claim, batch, items[0], "setup-complete", ToolCallBatchItemStateCompleted, map[string]any{"ok": true}, "")
			admission := orderedKernelAdmissionInput(t, batch, items[1])
			mutationClaim := claim
			var fail error
			switch scenario {
			case "hook-failure":
				fail = errors.New("controlled failure after start and operation insertion")
			case "wrong-proposal":
				admission.ProposalSHA256 = strings.Repeat("0", 64)
			case "wrong-ordinal":
				admission.Ordinal = 0
			case "wrong-owner":
				mutationClaim.OwnerID = "foreign-owner"
			case "wrong-batch":
				admission.BatchID = "foreign-batch"
			case "wrong-input-hash":
				admission.InputSHA256 = strings.Repeat("0", 64)
			case "wrong-input-size":
				admission.InputByteLength++
			}
			if scenario == "missing-receipt" || scenario == "missing-hook" {
				payload, _ := json.Marshal(map[string]any{"status": "running", "toolPhase": "start", "toolName": items[1].ToolName, "toolCallId": items[1].ToolCallID, "kernelAdmission": admission})
				checkpoint := transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "kernel-start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload}
				if scenario == "missing-receipt" {
					checkpoint.CommitHook = func(context.Context, *transcriptstore.ImmediateTransaction, transcriptstore.Event, bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
						return transcriptstore.RunnerCheckpointCommitReceipt{}, nil
					}
				}
				if _, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), checkpoint); err == nil {
					t.Fatal("admission checkpoint escaped receipt fence")
				}
			} else {
				if _, err := appendOrderedKernelStart(store, repo, mutationClaim, batch, items[1], admission, fail); err == nil {
					t.Fatal("invalid admission succeeded")
				}
			}
			state, err := store.KernelToolCallBatchAdmission(context.Background(), claim, batch.BatchID, 1)
			if err != nil || state.OperationFound || state.Item.State != ToolCallBatchItemStatePending || state.Item.StartedEventID != 0 {
				t.Fatalf("rollback state=%#v err=%v", state, err)
			}
			var events, approvals int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE client_message_id='kernel-start'`).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='kernel_local_exec_approval_requested'`).Scan(&approvals); err != nil {
				t.Fatal(err)
			}
			if events != 0 || approvals != 0 {
				t.Fatalf("partial commit: events=%d approvals=%d", events, approvals)
			}
		})
	}
}

func TestKernelBatchAdmissionLargeStartKeepsOneBody(t *testing.T) {
	store, repo, claim := newKernelLocalOperationFixture(t)
	batch, items := createOrderedKernelBatch(t, store, repo, claim, "python", strings.Repeat("value = 1\n", 60000))
	if len(items[1].ArgumentsJSON) <= 512<<10 {
		t.Fatal("fixture does not cross duplicate-body threshold")
	}
	batch, items[0] = appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "setup-start")
	batch, items[0] = appendToolCallBatchTerminal(t, store, repo, claim, batch, items[0], "setup-complete", ToolCallBatchItemStateCompleted, map[string]any{"ok": true}, "")
	operation, err := appendOrderedKernelStart(store, repo, claim, batch, items[1], orderedKernelAdmissionInput(t, batch, items[1]), nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := store.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE client_message_id='kernel-start'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) >= transcriptstore.MaxEventPayloadBytes || bytes.Count(payload, []byte("value = 1")) != 60000 {
		t.Fatalf("duplicated/truncated body size=%d occurrences=%d", len(payload), bytes.Count(payload, []byte("value = 1")))
	}
	if bytes.Count(operation.InputJSON, []byte("value = 1")) != 60000 {
		t.Fatal("canonical execution lost source bytes")
	}
}

func TestKernelBatchAdmissionReclaimsAcrossRunnerAttempts(t *testing.T) {
	for _, afterStart := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-admission", true: "after-admission-before-policy"}[afterStart], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "workspace.db")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			store, repo, claim := initializeKernelLocalOperationFixture(t, store)
			t.Cleanup(func() { _ = store.Close() })
			batch, items := createOrderedKernelBatch(t, store, repo, claim, "python")
			batch, items[0] = appendToolCallBatchStart(t, store, repo, claim, batch, items[0], "setup-start")
			batch, items[0] = appendToolCallBatchTerminal(t, store, repo, claim, batch, items[0], "setup-complete", ToolCallBatchItemStateCompleted, map[string]any{"ok": true}, "")
			if afterStart {
				if _, err := appendOrderedKernelStart(store, repo, claim, batch, items[1], orderedKernelAdmissionInput(t, batch, items[1]), nil); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := repo.FinishRunner(ctx, transcriptstore.FinishRunnerInput{Claim: claim, ClientMessageID: "controlled-handoff", Status: "failed", PayloadJSON: []byte(`{"status":"failed","reason":"controlled restart after durable checkpoint"}`)}); err != nil {
				t.Fatal(err)
			}
			checkpoint, found, err := repo.LatestResumableCheckpoint(ctx, claim.StreamUID, claim.OwnerID)
			if err != nil || !found {
				t.Fatalf("checkpoint=%#v found=%v err=%v", checkpoint, found, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			repo, err = store.TranscriptRepository(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target, err := store.GetCompatibilityFrameResumeTarget(ctx, claim.OwnerID, "frame")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ResumeCompatibilityFrameConversation("frame", ResumeCompatibilityFrameInput{AllowProcessingInterrupted: true, Command: &CompatibilityFrameContinueCommand{ActorID: claim.OwnerID, ClientCommandID: "9d212fa5-15db-4684-9de7-039afc138727", Target: target}}); err != nil {
				t.Fatal(err)
			}
			dispatch, found, err := store.ClaimNextCompatibilityFrameResumeDispatch("ordered-recovery", time.Minute)
			if err != nil || !found {
				t.Fatalf("dispatch=%#v found=%v err=%v", dispatch, found, err)
			}
			reclaimed, err := repo.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{StreamUID: claim.StreamUID, OwnerID: claim.OwnerID, RunnerID: "frame-resume:" + dispatch.ResumeEvent.ID, TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: checkpoint.Sequence})
			if err != nil || !reclaimed.Claimed || reclaimed.Claim.Attempt <= claim.Attempt {
				t.Fatalf("claim=%#v err=%v", reclaimed, err)
			}
			current, found, err := store.GetToolCallBatch(ctx, claim.OwnerID, batch.BatchID)
			if err != nil || !found {
				t.Fatal(err)
			}
			current, err = store.ClaimToolCallBatch(ctx, ClaimToolCallBatchInput{Claim: reclaimed.Claim, BatchID: batch.BatchID, ExpectedStateVersion: current.StateVersion})
			if err != nil {
				t.Fatal(err)
			}
			state, err := store.KernelToolCallBatchAdmission(ctx, reclaimed.Claim, batch.BatchID, 1)
			if err != nil {
				t.Fatal(err)
			}
			operation := state.Operation
			if !afterStart {
				operation, err = appendOrderedKernelStart(store, repo, reclaimed.Claim, current, state.Item, orderedKernelAdmissionInput(t, current, state.Item), nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if operation.SourceRunnerAttempt != claim.Attempt || operation.SourceEventID != batch.SourceEventID || operation.ToolCallOrdinal != 1 {
				t.Fatal("recovery rewrote immutable proposal origin")
			}
			approved, err := store.ResolveKernelLocalOperationApproval(ctx, ResolveKernelLocalOperationApprovalInput{OwnerUserID: claim.OwnerID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion, ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "recovered-policy", Scope: "once", Source: "policy", ActorID: "system", CurrentClaim: reclaimed.Claim})
			if err != nil {
				t.Fatal(err)
			}
			runnable, err := store.ListRunnableKernelLocalOperations(ctx, reclaimed.Claim, 10)
			if err != nil || len(runnable) != 1 || runnable[0].OperationID != approved.OperationID {
				t.Fatalf("runnable=%#v err=%v", runnable, err)
			}
			if _, err := store.KernelToolCallBatchAdmission(ctx, claim, batch.BatchID, 1); err == nil {
				t.Fatal("expired owner resumed current item")
			}
		})
	}
}

func createOrderedKernelBatch(t *testing.T, store *Store, repo *transcriptstore.Repository, claim transcriptstore.RunnerClaim, tool string, code ...string) (ToolCallBatch, []ToolCallBatchItem) {
	t.Helper()
	input := map[string]any{"environment": "requested-env", "code": "print(1)", "human_description": "Controlled calculation"}
	if len(code) > 0 {
		input["code"] = code[0]
	}
	if tool == "bash" {
		delete(input, "code")
		input["command"] = "printf controlled"
	}
	var payload map[string]any
	if err := json.Unmarshal(toolCallBatchRootPayload(t, toolCallBatchTestCall{id: "setup", name: "manage_environments", arguments: map[string]any{"mode": "create", "name": "requested-env"}}, toolCallBatchTestCall{id: "compute", name: tool, arguments: input}), &payload); err != nil {
		t.Fatal(err)
	}
	payload["kernelAdmissionVersion"] = KernelBatchAdmissionVersion
	raw, _ := json.Marshal(payload)
	var batch ToolCallBatch
	var items []ToolCallBatchItem
	_, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "ordered-proposal", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: raw,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			var err error
			batch, items, _, err = store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err := store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			if err != nil || len(operations) != 0 {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, errors.New("proposal prematurely admitted kernel")
			}
			return toolCallBatchTestCommitReceipt(batch, nil)
		}})
	if err != nil {
		t.Fatal(err)
	}
	batch, err = store.ClaimToolCallBatch(context.Background(), ClaimToolCallBatchInput{Claim: claim, BatchID: batch.BatchID, ExpectedStateVersion: batch.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	return batch, items
}

func orderedKernelAdmissionInput(t *testing.T, batch ToolCallBatch, item ToolCallBatchItem) KernelBatchAdmission {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal(item.ArgumentsJSON, &input); err != nil {
		t.Fatal(err)
	}
	input["environment"] = "verified-env"
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	return KernelBatchAdmission{Version: KernelBatchAdmissionVersion, BatchID: batch.BatchID, Ordinal: item.Ordinal, ProposalSHA256: item.ArgumentsSHA256, InputSHA256: hex.EncodeToString(digest[:]), InputByteLength: len(raw)}
}

func appendOrderedKernelStart(store *Store, repo *transcriptstore.Repository, claim transcriptstore.RunnerClaim, batch ToolCallBatch, item ToolCallBatchItem, admission KernelBatchAdmission, fail error) (KernelLocalOperation, error) {
	var input map[string]any
	if err := json.Unmarshal(item.ArgumentsJSON, &input); err != nil {
		return KernelLocalOperation{}, err
	}
	input["environment"] = "verified-env"
	canonical, _ := json.Marshal(input)
	payload, _ := json.Marshal(map[string]any{"status": "running", "toolPhase": "start", "toolName": item.ToolName, "toolCallId": item.ToolCallID, "toolInput": json.RawMessage(item.ArgumentsJSON), "kernelAdmission": admission})
	var operation KernelLocalOperation
	_, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{Claim: claim, ClientMessageID: "kernel-start", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			_, _, err := store.StartToolCallBatchItemTx(ctx, tx, StartToolCallBatchItemInput{Claim: claim, BatchID: batch.BatchID, Ordinal: item.Ordinal, ExpectedBatchStateVersion: batch.StateVersion, ExpectedItemStateVersion: item.StateVersion, StartedEvent: event})
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operation, err = store.CreateKernelLocalOperationForBatchStartTx(ctx, tx, claim, event, canonical)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			if fail != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, fail
			}
			return transcriptstore.RunnerCheckpointCommitReceipt{KernelOperations: []transcriptstore.RunnerCheckpointKernelOperationReceipt{{OperationID: operation.OperationID, Ordinal: operation.ToolCallOrdinal, ToolCallID: operation.ToolCallID, Tool: operation.Tool, InputSHA256: operation.InputSHA256, ApprovalRequestID: operation.ApprovalRequestID, RequestedEventID: "kernel-local-operation-requested:" + operation.OperationID, InitialStateVersion: 1}}}, nil
		}})
	return operation, err
}
