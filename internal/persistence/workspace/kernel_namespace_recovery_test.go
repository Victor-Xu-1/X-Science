package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func startedNamespaceRecoveryFixture(t *testing.T) (*Store, KernelExecutionBackend, DetachedKernelExecution, MarkKernelExecutionNamespaceRecoveryInput) {
	t.Helper()
	ctx := context.Background()
	store, backend, lease, execution := newAcceptedDetachedKernelExecutionFixture(t)
	control := KernelDetachedExecutionControlInput{ExecutionID: execution.ExecutionID, BackendGeneration: backend.BackendGeneration, ControllerEpoch: lease.Epoch, ControllerToken: lease.Token, ExpectedVersion: execution.StateVersion}
	var err error
	execution, err = store.CommitKernelExecutionDispatch(ctx, CommitKernelExecutionDispatchInput{KernelDetachedExecutionControlInput: control, DispatchSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	control.ExpectedVersion = execution.StateVersion
	execution, err = store.MarkKernelExecutionRequestWritten(ctx, MarkKernelExecutionRequestWrittenInput{KernelDetachedExecutionControlInput: control, DispatchSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	execution, err = store.MarkKernelExecutionStarted(ctx, MarkKernelExecutionStartedInput{ExecutionID: execution.ExecutionID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, ExpectedVersion: execution.StateVersion, ObservationSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	return store, backend, execution, MarkKernelExecutionNamespaceRecoveryInput{OwnerUserID: "owner", OperationID: execution.OperationID, ExecutionID: execution.ExecutionID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, ExpectedVersion: execution.StateVersion}
}

func TestNamespaceRecoveryMarkerPreservesStartedExecutionAndNotifiesOnce(t *testing.T) {
	ctx := context.Background()
	store, backend, before, input := startedNamespaceRecoveryFixture(t)
	for name, mutate := range map[string]func(*MarkKernelExecutionNamespaceRecoveryInput){"owner": func(v *MarkKernelExecutionNamespaceRecoveryInput) { v.OwnerUserID = "foreign" }, "operation": func(v *MarkKernelExecutionNamespaceRecoveryInput) { v.OperationID = "other" }, "executor": func(v *MarkKernelExecutionNamespaceRecoveryInput) { v.ExecutorInstanceID = "other" }, "generation": func(v *MarkKernelExecutionNamespaceRecoveryInput) { v.BackendGeneration++ }, "version": func(v *MarkKernelExecutionNamespaceRecoveryInput) { v.ExpectedVersion-- }} {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			if _, err := store.MarkKernelExecutionNamespaceRecovery(ctx, changed); err == nil {
				t.Fatal("stale/foreign marker admitted")
			}
		})
	}
	marked, err := store.MarkKernelExecutionNamespaceRecovery(ctx, input)
	if err != nil || marked.State != DetachedKernelExecutionStateStarted || marked.TerminalReceiptID != "" || marked.ReasonCode != KernelExecutionNamespaceRecoveryReasonCode || marked.StateVersion != before.StateVersion+1 {
		t.Fatalf("marked=%#v err=%v", marked, err)
	}
	again, err := store.MarkKernelExecutionNamespaceRecovery(ctx, input)
	if err != nil || again.StateVersion != marked.StateVersion {
		t.Fatalf("replay=%#v err=%v", again, err)
	}
	stale := input
	stale.ExpectedVersion--
	if _, err := store.MarkKernelExecutionNamespaceRecovery(ctx, stale); err == nil {
		t.Fatal("older CAS version admitted after marker")
	}
	operation, found, err := store.GetKernelLocalOperation(ctx, "owner", before.OperationID)
	if err != nil || !found || operation.State != KernelLocalOperationStateStarted || operation.TerminalAt != nil || len(operation.ResultJSON) != 0 {
		t.Fatalf("operation=%#v found=%v err=%v", operation, found, err)
	}
	var notifications, receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE notification_type='execution_recovery_required'`).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatalf("notifications=%d err=%v", notifications, err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM kernel_execution_result_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("terminal receipts=%d err=%v", receipts, err)
	}
	page, err := store.ListDetachedKernelExecutionRecoveryPage(ctx, "", 10)
	if err != nil || len(page.Candidates) != 0 {
		t.Fatalf("recovery repeatedly observed unknown runtime: %#v err=%v", page, err)
	}
	resultJSON := `{"ok":true}`
	digest := sha256.Sum256([]byte(resultJSON))
	now := time.Now().UTC()
	terminal, _, err := store.CommitKernelExecutionResult(ctx, CommitKernelExecutionResultInput{ReceiptID: "actual-later-receipt", ExecutionID: before.ExecutionID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, TerminalSequence: 2, Outcome: KernelExecutionResultCompleted, ResultJSON: resultJSON, ResultSHA256: hex.EncodeToString(digest[:]), ResultRef: "kernel-spool-sha256:" + strings.Repeat("f", 64), StartedAt: now.Add(-time.Second), FinishedAt: now, FilesWrittenJSON: `[]`, DroppedRootsJSON: `[]`})
	if err != nil || terminal.State != DetachedKernelExecutionStateTerminal {
		t.Fatalf("actual later receipt suppressed: %#v err=%v", terminal, err)
	}
	page, err = store.ListDetachedKernelExecutionRecoveryPage(ctx, "", 10)
	if err != nil || len(page.Candidates) != 1 {
		t.Fatalf("actual terminal receipt ineligible: %#v err=%v", page, err)
	}
	input.ExpectedVersion = terminal.StateVersion
	if _, err := store.MarkKernelExecutionNamespaceRecovery(ctx, input); err == nil {
		t.Fatal("terminal execution regressed to recovery")
	}
}

func TestNamespaceRecoveryNotificationFailureRollsBackMarker(t *testing.T) {
	ctx := context.Background()
	store, _, before, input := startedNamespaceRecoveryFixture(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_namespace_notification BEFORE INSERT ON notifications WHEN NEW.notification_type='execution_recovery_required' BEGIN SELECT RAISE(ABORT,'injected namespace notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkKernelExecutionNamespaceRecovery(ctx, input); err == nil {
		t.Fatal("injected notification failure succeeded")
	}
	after, found, err := store.GetDetachedKernelExecution(ctx, before.ExecutionID)
	if err != nil || !found || after.ReasonCode != before.ReasonCode || after.StateVersion != before.StateVersion || after.State != before.State {
		t.Fatalf("partial marker=%#v found=%v err=%v", after, found, err)
	}
}
