//go:build linux

package detached

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestReaderFirstExecutorRequiresOriginalV2Owner(t *testing.T) {
	contract := workspace.KernelExecutionResultContract{ResultVersion: 2, HostResourceEvidenceVersion: 1}
	doc := workspace.KernelDetachedExecutionDocument{DocumentVersion: 2, ResultContract: contract,
		Binding: workspace.KernelDetachedExecutionBindingV2{BackendID: "backend", BackendGeneration: 7, ExecutorInstanceID: "original-executor", SessionSpecSHA256: strings.Repeat("a", 64)}}
	e := Executor{BackendID: doc.Binding.BackendID, BackendGeneration: doc.Binding.BackendGeneration, ExecutorInstanceID: doc.Binding.ExecutorInstanceID,
		sessionDocument: workspace.KernelExecutionSessionDocument{DocumentVersion: 2, ResultContract: contract}, sessionSpecSHA256: doc.Binding.SessionSpecSHA256}
	if !e.acceptsExecutionDocument(doc) {
		t.Fatal("same owner cannot continue its already authorized V2 request")
	}
	for _, field := range []string{"backend", "generation", "instance", "session", "contract", "legacy_session"} {
		t.Run(field, func(t *testing.T) {
			changed := Executor{BackendID: doc.Binding.BackendID, BackendGeneration: doc.Binding.BackendGeneration, ExecutorInstanceID: doc.Binding.ExecutorInstanceID,
				sessionDocument: workspace.KernelExecutionSessionDocument{DocumentVersion: 2, ResultContract: contract}, sessionSpecSHA256: doc.Binding.SessionSpecSHA256}
			switch field {
			case "backend":
				changed.BackendID = "another-backend"
			case "generation":
				changed.BackendGeneration++
			case "instance":
				changed.ExecutorInstanceID = "replacement-executor"
			case "session":
				changed.sessionSpecSHA256 = strings.Repeat("b", 64)
			case "contract":
				changed.sessionDocument.ResultContract.ResultVersion = 3
			case "legacy_session":
				changed.sessionDocument.DocumentVersion = 1
			}
			if changed.acceptsExecutionDocument(doc) {
				t.Fatalf("%s acquired an existing V2 request", field)
			}
		})
	}
	if e.acceptsExecutionDocument(workspace.KernelDetachedExecutionDocument{DocumentVersion: 1}) {
		t.Fatal("V1 request silently downgraded a V2 session")
	}
	e.sessionDocument.DocumentVersion = 1
	if !e.acceptsExecutionDocument(workspace.KernelDetachedExecutionDocument{DocumentVersion: 1}) {
		t.Fatal("V1 production path changed")
	}
}

func TestReaderFirstCancellationRejectsForeignV2BeforeDraining(t *testing.T) {
	now := time.Now().UTC()
	result := ExecutionResultV1{Version: 1, ExecutionID: "execution", ToolCallID: "tool", KernelID: "kernel", KernelKind: "analysis", Language: "python", Environment: "python", StartedAt: now, FinishedAt: now}
	execution, _ := resultAuthorityForTest(t, result, 2)
	doc, err := workspace.ReadDetachedExecutionDocument(execution)
	if err != nil {
		t.Fatal(err)
	}
	execution.TerminalReceiptID, execution.State, execution.CancelRequestID = "", workspace.DetachedKernelExecutionStateCancelRequested, "cancel"
	execution.WorkerStartedAt = &now
	e := Executor{BackendID: execution.BackendID, BackendGeneration: execution.BackendGeneration, ExecutorInstanceID: "foreign-executor",
		sessionDocument: workspace.KernelExecutionSessionDocument{DocumentVersion: 2, ResultContract: doc.ResultContract}, sessionSpecSHA256: doc.Binding.SessionSpecSHA256}
	_, err = e.applyDurableCancellation(context.Background(), execution)
	if !errors.Is(err, workspace.ErrDetachedKernelExecutionConflict) || e.draining {
		t.Fatalf("foreign owner changed cancellation state: draining=%t err=%v", e.draining, err)
	}
}
