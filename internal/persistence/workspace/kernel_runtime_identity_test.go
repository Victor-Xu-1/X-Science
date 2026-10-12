package workspace

import (
	"context"
	"strings"
	"testing"
)

func startedRuntimeIdentityFixture(t *testing.T) (*Store, KernelLocalOperation, KernelRuntimeIdentityReceipt) {
	t.Helper()
	ctx := context.Background()
	store, repository, claim := newKernelLocalOperationFixture(t)
	operation := createKernelLocalOperationForTest(t, store, repository, claim, "runtime-identity", "call-runtime-identity")
	approved, err := store.ResolveKernelLocalOperationApproval(ctx, ResolveKernelLocalOperationApprovalInput{
		OwnerUserID: "owner", OperationID: operation.OperationID, ExpectedStateVersion: operation.StateVersion,
		ApprovalRequestID: operation.ApprovalRequestID, Approved: true, DecisionID: "decision-runtime-identity",
		Scope: "once", Source: "user", ActorID: "owner", CurrentClaim: claim,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareKernelLocalOperation(ctx, PrepareKernelLocalOperationInput{
		OwnerUserID: "owner", OperationID: operation.OperationID, ExpectedStateVersion: approved.StateVersion,
		Claim: claim, BootID: "boot", KernelID: "kernel", KernelGeneration: 1, ConfinementSHA256: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.StartKernelLocalOperation(ctx, StartKernelLocalOperationInput{
		OwnerUserID: "owner", OperationID: operation.OperationID, ExpectedStateVersion: prepared.StateVersion,
		Claim: claim, BootID: "boot", ExecutionID: "execution-runtime-identity",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, started, KernelRuntimeIdentityReceipt{OperationID: started.OperationID, ExecutionID: started.ExecutionID,
		InputSHA256: started.InputSHA256, RuntimeKind: "docker-container", RuntimeID: "observed-container-id",
		Environment: started.Environment, EnvironmentGeneration: strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("c", 64)}
}

func TestKernelRuntimeIdentityIsOwnerExecutionBoundAndImmutable(t *testing.T) {
	ctx := context.Background()
	store, operation, input := startedRuntimeIdentityFixture(t)
	first, err := store.RecordKernelRuntimeIdentity(ctx, "owner", input)
	if err != nil || first.CreatedAtMS <= 0 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := store.RecordKernelRuntimeIdentity(ctx, "owner", input)
	if err != nil || second != first {
		t.Fatalf("replay=%#v err=%v", second, err)
	}
	if _, found, err := store.GetKernelRuntimeIdentity(ctx, "foreign-owner", operation.OperationID); err != nil || found {
		t.Fatalf("foreign owner found=%t err=%v", found, err)
	}
	if _, err := store.RecordKernelRuntimeIdentity(ctx, "foreign-owner", input); err == nil {
		t.Fatal("foreign owner wrote a runtime receipt")
	}
	for name, mutate := range map[string]func(*KernelRuntimeIdentityReceipt){
		"execution":   func(value *KernelRuntimeIdentityReceipt) { value.ExecutionID = "different-execution" },
		"input":       func(value *KernelRuntimeIdentityReceipt) { value.InputSHA256 = strings.Repeat("d", 64) },
		"runtime":     func(value *KernelRuntimeIdentityReceipt) { value.RuntimeID = "different-runtime" },
		"environment": func(value *KernelRuntimeIdentityReceipt) { value.Environment = "different-environment" },
		"generation":  func(value *KernelRuntimeIdentityReceipt) { value.EnvironmentGeneration = strings.Repeat("e", 64) },
		"image":       func(value *KernelRuntimeIdentityReceipt) { value.ImageID = "sha256:" + strings.Repeat("f", 64) },
		"origin":      func(value *KernelRuntimeIdentityReceipt) { value.NamespaceOrigin = "pre-cutover" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			if _, err := store.RecordKernelRuntimeIdentity(ctx, "owner", changed); err == nil {
				t.Fatal("changed identity admitted")
			}
		})
	}
	if _, err := store.db.Exec(`UPDATE kernel_runtime_identity_receipts SET runtime_id='replacement'`); err == nil {
		t.Fatal("runtime receipt was rewritten")
	}
	retained, found, err := store.GetKernelLocalOperation(ctx, "owner", operation.OperationID)
	if err != nil || !found || retained.State != KernelLocalOperationStateStarted || len(retained.ResultJSON) != 0 {
		t.Fatalf("operation was prematurely completed: %#v err=%v", retained, err)
	}
}

func TestKernelRuntimeIdentityWriteFailureCannotInventDetachedReceipt(t *testing.T) {
	ctx := context.Background()
	store, operation, input := startedRuntimeIdentityFixture(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_runtime_identity BEFORE INSERT ON kernel_runtime_identity_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordKernelRuntimeIdentity(ctx, "owner", input); err == nil {
		t.Fatal("injected receipt write succeeded")
	}
	if _, found, err := store.GetKernelRuntimeIdentity(ctx, "owner", operation.OperationID); err != nil || found {
		t.Fatalf("invented receipt found=%t err=%v", found, err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_runtime_identity`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE kernel_local_operations SET state='outcome_unknown',terminal_at=started_at,state_version=state_version+1 WHERE operation_id=?`, operation.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordKernelRuntimeIdentity(ctx, "owner", input); err == nil {
		t.Fatal("late runtime identity wrote after terminal fencing")
	}
}
