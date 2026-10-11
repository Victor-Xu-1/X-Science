package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDetachedRecoveryFindsSameBootUnsettledExecutions(t *testing.T) {
	store, _, _, execution := newAcceptedDetachedKernelExecutionFixture(t)
	page, err := store.ListDetachedKernelExecutionRecoveryPage(context.Background(), "", 10)
	if err != nil || len(page.Candidates) != 1 || page.Candidates[0].Execution.ExecutionID != execution.ExecutionID {
		t.Fatalf("same-boot unfinished execution cannot be reconciled: page=%#v err=%v", page, err)
	}
}

func TestDetachedRecoveryKeysetPassesAnUnchangedFullPage(t *testing.T) {
	store, repo, claim := newKernelLocalOperationFixture(t)
	for index := range 129 {
		acceptDetachedKernelExecutionFixture(t, store, repo, claim, fmt.Sprintf("-%03d", index))
	}
	page, err := store.ListDetachedKernelExecutionRecoveryPage(context.Background(), "", 128)
	if err != nil || len(page.Candidates) != 128 || page.NextCursor == "" {
		t.Fatalf("first page=%#v err=%v", page, err)
	}
	// An observer reservation or a transient socket failure does not change any
	// row. The next page must still reach the last execution without spinning.
	last, err := store.ListDetachedKernelExecutionRecoveryPage(context.Background(), page.NextCursor, 128)
	if err != nil || len(last.Candidates) != 1 || last.NextCursor != "" || last.Candidates[0].Execution.ExecutionID != "execution-detached-lifecycle-128" {
		t.Fatalf("later execution starved: page=%#v err=%v", last, err)
	}
}

func TestDetachedRecoveryEvidenceLossRemainsRecoverableAndFenced(t *testing.T) {
	ctx := context.Background()
	store, backend, _, execution := newAcceptedDetachedKernelExecutionFixture(t)
	op, _, err := store.GetKernelLocalOperation(ctx, "owner", execution.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	input := MarkKernelLocalOperationOutcomeUnknownInput{OwnerUserID: op.OwnerUserID, OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion, ExecutionID: op.ExecutionID, CurrentBootID: op.BootID, ReasonCode: "detached_backend_evidence_lost"}
	fence := DetachedKernelEvidenceLossFence{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, RequestSHA256: execution.RequestSHA256, ConfinementSHA256: execution.ConfinementSHA256}
	if _, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, fence); err == nil {
		t.Fatal("running backend authorized evidence loss")
	}
	if _, err := store.FinishKernelExecutionBackend(ctx, FinishKernelExecutionBackendInput{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, EvidenceLost: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkKernelExecutionEvidenceLost(ctx, MarkKernelExecutionEvidenceLostInput{ExecutionID: execution.ExecutionID, BackendGeneration: backend.BackendGeneration, ReasonCode: input.ReasonCode}); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart/failure between physical evidence loss and operation
	// materialization. The partial settlement must stay in the recovery scan.
	page, err := store.ListDetachedKernelExecutionRecoveryPage(ctx, "", 10)
	if err != nil || len(page.Candidates) != 1 {
		t.Fatalf("partial evidence loss disappeared: %#v %v", page, err)
	}
	if _, err := store.MarkKernelLocalOperationOutcomeUnknown(ctx, input); !errors.Is(err, ErrKernelLocalOperationStale) {
		t.Fatalf("generic same-boot recovery weakened: %v", err)
	}
	wrong := fence
	wrong.ExecutorInstanceID = "different-executor"
	if _, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, wrong); !errors.Is(err, ErrKernelExecutionBackendStale) {
		t.Fatalf("wrong executor authorized: %v", err)
	}
	for name, mutate := range map[string]func(*DetachedKernelEvidenceLossFence){
		"backend":     func(f *DetachedKernelEvidenceLossFence) { f.BackendID += "-other" },
		"generation":  func(f *DetachedKernelEvidenceLossFence) { f.BackendGeneration++ },
		"request":     func(f *DetachedKernelEvidenceLossFence) { f.RequestSHA256 = strings.Repeat("f", 64) },
		"confinement": func(f *DetachedKernelEvidenceLossFence) { f.ConfinementSHA256 = strings.Repeat("a", 64) },
	} {
		wrong := fence
		mutate(&wrong)
		if _, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, wrong); !errors.Is(err, ErrDetachedKernelExecutionConflict) {
			t.Fatalf("%s fence accepted: %v", name, err)
		}
	}
	repo, err := store.TranscriptRepository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := repo.GetFrameStreamBySession(ctx, "owner", op.FrameID)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, fence)
	if err != nil || settled.State != KernelLocalOperationStateOutcomeUnknown {
		t.Fatalf("settle=%#v err=%v", settled, err)
	}
	if replay, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, fence); err != nil || replay.StateVersion != settled.StateVersion {
		t.Fatalf("settlement replay=%#v err=%v", replay, err)
	}
	after, _, err := repo.GetFrameStreamBySession(ctx, "owner", op.FrameID)
	if err != nil || before.InputRevision != after.InputRevision {
		t.Fatalf("live source runner was superseded: before=%d after=%d err=%v", before.InputRevision, after.InputRevision, err)
	}
	var materializations int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operation_materializations WHERE operation_id=?`, op.OperationID).Scan(&materializations); err != nil || materializations != 1 {
		t.Fatalf("materializations=%d err=%v", materializations, err)
	}
	page, err = store.ListDetachedKernelExecutionRecoveryPage(ctx, "", 10)
	if err != nil || len(page.Candidates) != 0 {
		t.Fatalf("settled operation still eligible: %#v %v", page, err)
	}
}

func TestDetachedRecoveryDoesNotRequeueCancelledFrame(t *testing.T) {
	ctx := context.Background()
	store, backend, _, execution := newAcceptedDetachedKernelExecutionFixture(t)
	op, _, err := store.GetKernelLocalOperation(ctx, "owner", execution.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CancelFrameWithTranscript(ctx, op.FrameID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishKernelExecutionBackend(ctx, FinishKernelExecutionBackendInput{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, EvidenceLost: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkKernelExecutionEvidenceLost(ctx, MarkKernelExecutionEvidenceLostInput{ExecutionID: execution.ExecutionID, BackendGeneration: backend.BackendGeneration, ReasonCode: "detached_backend_evidence_lost"}); err != nil {
		t.Fatal(err)
	}
	input := MarkKernelLocalOperationOutcomeUnknownInput{OwnerUserID: op.OwnerUserID, OperationID: op.OperationID, ExpectedStateVersion: op.StateVersion, ExecutionID: op.ExecutionID, CurrentBootID: op.BootID, ReasonCode: "detached_backend_evidence_lost"}
	fence := DetachedKernelEvidenceLossFence{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, RequestSHA256: execution.RequestSHA256, ConfinementSHA256: execution.ConfinementSHA256}
	if _, err := store.MarkDetachedKernelLocalOperationOutcomeUnknown(ctx, input, fence); err != nil {
		t.Fatal(err)
	}
	frame, found, err := store.GetFrame(op.FrameID)
	if err != nil || !found || frame.Status != FrameStatusCancelled {
		t.Fatalf("cancelled frame revived: %#v %v", frame, err)
	}
}
