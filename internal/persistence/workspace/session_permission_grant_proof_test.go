package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestSessionPermissionGrantLegacyRecoveryCannotRenewOrResurrect(t *testing.T) {
	for _, change := range []string{"consume-preserves-generation", "revoke-before-consume", "renew-before-consume"} {
		t.Run(change, func(t *testing.T) {
			store, _, request := newKernelLocalExecApprovalFixture(t)
			id := approveSessionGrantForTest(t, store, request, "conversation")
			policy, err := store.KernelLocalExecApprovalPolicy(context.Background(), request.OwnerUserID, request.ProjectID, request.RootFrameID, request.Tool, request.Environment)
			if err != nil || policy.GrantProof == nil {
				t.Fatalf("actual proof=%#v err=%v", policy, err)
			}
			pending := request
			pending.RequestID, pending.ToolCallID = "pending-recovery", "pending-recovery-call"
			if _, err := store.AddKernelLocalExecApprovalRequest(context.Background(), pending); err != nil {
				t.Fatal(err)
			}
			if change == "revoke-before-consume" {
				if _, err := store.RevokeSessionPermissionGrants(context.Background(), sessionGrantRevokeForTest(request, []SessionPermissionGrantSelection{{GrantID: id, Kind: "local_exec", Key: request.Tool}})); err != nil {
					t.Fatal(err)
				}
			}
			if change == "renew-before-consume" {
				renewal := request
				renewal.RequestID, renewal.ToolCallID = "renew-before-consume", "renew-before-consume-call"
				id = approveSessionGrantForTest(t, store, renewal, "conversation")
			}
			resolution := kernelLocalExecResolutionForTest(pending, true, "conversation")
			resolution.ApprovalSource, resolution.GrantProof = "remembered", policy.GrantProof
			_, _, created, err := store.ResolveKernelLocalExecApproval(context.Background(), resolution)
			if change != "consume-preserves-generation" {
				if !errors.Is(err, ErrApprovalPolicyGrantStale) || created {
					t.Fatalf("revoked proof admitted recovery: created=%t err=%v", created, err)
				}
				snapshot, readErr := store.SessionPermissionGrants(context.Background(), request.OwnerUserID, request.FrameID, nil)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if change == "revoke-before-consume" && len(snapshot.Grants) != 0 {
					t.Fatalf("recovery resurrected grant=%#v", snapshot)
				}
				if change == "renew-before-consume" && (len(snapshot.Grants) != 1 || snapshot.Grants[0].GrantID != id) {
					t.Fatalf("recovery replaced renewal=%#v", snapshot)
				}
				if _, found, readErr := store.GetKernelLocalExecApprovalDecision(context.Background(), request.OwnerUserID, request.ProjectID, request.FrameID, request.FrameIncarnationID, pending.RequestID); readErr != nil || found {
					t.Fatalf("stale proof left decision: found=%t err=%v", found, readErr)
				}
			} else {
				if err != nil || !created {
					t.Fatalf("valid recovery failed: created=%t err=%v", created, err)
				}
				if current := sessionGrantIDForTest(t, store, request, "conversation"); current != id {
					t.Fatal("policy consumption renewed generation")
				}
				assertSessionGrantEvents(t, store, request, 1)
			}
		})
	}
}

func TestSessionPermissionGrantOperationProofFencesRevocationAndKeepsAdmittedWork(t *testing.T) {
	store, repo, claim := newKernelLocalOperationFixture(t)
	operations := sessionGrantOperationsForTest(t, store, repo, claim)
	first, second, pending := operations[0], operations[1], operations[2]
	approved, err := store.ResolveKernelLocalOperationApproval(context.Background(), sessionGrantOperationApproval(first, claim, "first-decision", "user", nil))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.KernelLocalExecApprovalPolicy(context.Background(), first.OwnerUserID, first.ProjectID, first.RootFrameID, first.Tool, first.Environment)
	if err != nil || policy.GrantProof == nil {
		t.Fatalf("proof=%#v err=%v", policy, err)
	}
	consumed, err := store.ResolveKernelLocalOperationApproval(context.Background(), sessionGrantOperationApproval(second, claim, "consumer-decision", "remembered", policy.GrantProof))
	if err != nil || consumed.State != KernelLocalOperationStateApproved {
		t.Fatalf("valid proof consumption=%#v err=%v", consumed, err)
	}
	unchanged, err := store.KernelLocalExecApprovalPolicy(context.Background(), first.OwnerUserID, first.ProjectID, first.RootFrameID, first.Tool, first.Environment)
	if err != nil || unchanged.GrantProof == nil || unchanged.GrantProof.ID != policy.GrantProof.ID {
		t.Fatal("remembered operation renewed permission")
	}
	result, err := store.RevokeSessionPermissionGrants(context.Background(), SessionPermissionGrantRevokeInput{
		OwnerID: first.OwnerUserID, FrameID: first.FrameID, Expected: SessionPermissionExpectedScope{FrameIncarnationID: first.FrameIncarnationID, RootFrameID: first.RootFrameID, RootFrameIncarnationID: first.RootFrameIncarnationID},
		Grants: []SessionPermissionGrantSelection{{GrantID: policy.GrantProof.ID, Kind: "local_exec", Key: first.Tool}},
	})
	if err != nil || result.Results[0].Status != "revoked" {
		t.Fatalf("revoke=%#v err=%v", result, err)
	}
	for _, source := range []string{"remembered", "policy"} {
		input := sessionGrantOperationApproval(pending, claim, "late-decision", source, policy.GrantProof)
		if source == "policy" {
			input.Scope = "once"
		}
		if _, err := store.ResolveKernelLocalOperationApproval(context.Background(), input); !errors.Is(err, ErrApprovalPolicyGrantStale) {
			t.Fatalf("%s bypassed final proof CAS: %v", source, err)
		}
	}
	for _, prior := range []KernelLocalOperation{approved, consumed} {
		now, found, err := store.GetKernelLocalOperation(context.Background(), prior.OwnerUserID, prior.OperationID)
		if err != nil || !found || now.State != prior.State || now.StateVersion != prior.StateVersion {
			t.Fatalf("revoke changed admitted work: before=%#v after=%#v err=%v", prior, now, err)
		}
	}
	now, found, err := store.GetKernelLocalOperation(context.Background(), pending.OwnerUserID, pending.OperationID)
	if err != nil || !found || now.State != KernelLocalOperationStatePendingApproval || now.StateVersion != pending.StateVersion {
		t.Fatalf("stale proof mutated pending operation=%#v err=%v", now, err)
	}
}

func sessionGrantOperationApproval(operation KernelLocalOperation, claim transcriptstore.RunnerClaim, decision, source string, proof *ApprovalPolicyGrantProof) ResolveKernelLocalOperationApprovalInput {
	return ResolveKernelLocalOperationApprovalInput{OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: decision, Scope: "conversation", Source: source, ActorID: "owner", CurrentClaim: claim, GrantProof: proof}
}

func sessionGrantOperationsForTest(t *testing.T, store *Store, repo *transcriptstore.Repository, claim transcriptstore.RunnerClaim) []KernelLocalOperation {
	t.Helper()
	calls := []any{}
	for _, id := range []string{"first-grant-call", "consumer-call", "late-call"} {
		calls = append(calls, map[string]any{"id": id, "type": "function", "name": "python", "arguments": map[string]any{"code": "print(1)", "environment": "science"}})
	}
	payload, err := json.Marshal(map[string]any{"status": "running", "modelToolCalls": calls})
	if err != nil {
		t.Fatal(err)
	}
	var operations []KernelLocalOperation
	_, _, _, err = repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "session-grant-tools", Phase: transcriptstore.RunnerPhaseExecuting, Resumable: true, PayloadJSON: payload,
		CommitHook: func(ctx context.Context, tx *transcriptstore.ImmediateTransaction, event transcriptstore.Event, _ bool) (transcriptstore.RunnerCheckpointCommitReceipt, error) {
			batch, _, _, err := store.CreateToolCallBatchForCheckpointTx(ctx, tx, event)
			if err != nil {
				return transcriptstore.RunnerCheckpointCommitReceipt{}, err
			}
			operations, err = store.CreateKernelLocalOperationsForCheckpointTx(ctx, tx, event)
			return kernelLocalOperationCommitReceiptWithBatch(batch, operations), err
		},
	})
	if err != nil || len(operations) != 3 {
		t.Fatalf("canonical operation batch=%#v err=%v", operations, err)
	}
	return operations
}
