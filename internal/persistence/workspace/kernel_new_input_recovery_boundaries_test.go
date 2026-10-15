package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestCanonicalKernelRecoveryComposerInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want bool
	}{
		{"composer", func(map[string]any) {}, true},
		{"attachment", func(m map[string]any) { m["artifactRefs"] = []any{map[string]any{"artifactId": "file"}} }, true},
		{"multimodal", func(m map[string]any) {
			m["content"] = []any{map[string]any{"type": "text", "text": "new intent"}, map[string]any{"type": "image", "source": "artifact"}}
		}, true},
		{"ask_user_aggregate", func(m map[string]any) { delete(m, "messageUuid"); delete(m, "_uuid"); delete(m, "clientMessageId") }, false},
		{"edited_ask_user", func(m map[string]any) {
			m["content"] = []any{map[string]any{"type": "tool_result", "tool_use_id": "ask", "content": "new intent"}}
		}, false},
		{"mixed_tool_result", func(m map[string]any) {
			m["content"] = append(m["content"].([]any), map[string]any{"type": "tool_result"})
		}, false},
		{"wrong_client", func(m map[string]any) { m["clientMessageId"] = "other" }, false},
		{"wrong_identity", func(m map[string]any) { m["_uuid"] = "other" }, false},
		{"wrong_origin", func(m map[string]any) { m["messageOrigin"] = "task_intent" }, false},
		{"empty_text_not_composer_contract", func(m map[string]any) { m["text"] = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := map[string]any{"messageUuid": "uuid", "_uuid": "uuid", "clientMessageId": "client", "role": "user", "messageOrigin": "input_response", "text": "new intent", "content": []any{map[string]any{"type": "text", "text": "new intent"}}}
			tc.edit(message)
			payload, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if got := transcriptstore.IsCanonicalFrameUserMessage("user_input_response", "client", payload); got != tc.want {
				t.Fatalf("canonical=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestKernelRecoveryNewInputPreservesExecutionAndClaimFences(t *testing.T) {
	for _, boundary := range []string{"same_revision", "prepared", "started", "stale_claim"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			store, repo, prior := newKernelLocalOperationFixture(t)
			operation := createKernelLocalOperationForTest(t, store, repo, prior, "old-proposal", "old-call")
			operation, err := store.ResolveKernelLocalOperationApproval(ctx, ResolveKernelLocalOperationApprovalInput{
				OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
				ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "old-approval", Scope: "once", Source: "policy", ActorID: "system", CurrentClaim: prior,
			})
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "prepared" || boundary == "started" {
				operation, err = store.PrepareKernelLocalOperation(ctx, PrepareKernelLocalOperationInput{
					OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					Claim: prior, BootID: "boot", KernelID: "kernel", KernelGeneration: 1, ConfinementSHA256: strings.Repeat("a", 64),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if boundary == "started" {
				operation, err = store.StartKernelLocalOperation(ctx, StartKernelLocalOperationInput{
					OwnerUserID: operation.OwnerUserID, OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
					Claim: prior, BootID: "boot", ExecutionID: "execution",
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			claim := prior
			if boundary != "same_revision" {
				claim = kernelRecoveryComposerInput(t, store, repo, prior, "input_response")
			}
			before, found, err := store.GetKernelLocalOperation(ctx, operation.OwnerUserID, operation.OperationID)
			if err != nil || !found {
				t.Fatalf("operation found=%t err=%v", found, err)
			}
			if boundary == "stale_claim" {
				claim.ClaimToken = "wrong-token"
			}
			_, err = store.ListRunnableKernelLocalOperations(ctx, claim, 10)
			if (err != nil) != (boundary == "stale_claim") {
				t.Fatalf("recovery error=%v", err)
			}
			after, found, err := store.GetKernelLocalOperation(ctx, operation.OwnerUserID, operation.OperationID)
			if err != nil || !found || after.State != before.State || after.StateVersion != before.StateVersion || after.ExecutionID != before.ExecutionID {
				t.Fatalf("protected operation changed: before=%s/%d after=%s/%d err=%v", before.State, before.StateVersion, after.State, after.StateVersion, err)
			}
			if _, found, err := store.GetKernelToolResultMaterialization(ctx, operation.OperationID); err != nil || found {
				t.Fatalf("protected work gained a synthetic result: found=%t err=%v", found, err)
			}
		})
	}
}
