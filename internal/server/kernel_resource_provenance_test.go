package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	kerneldetached "x-science/internal/kernel/detached"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/processsupervisor"
)

func TestGuestResourceTraceDoesNotBecomeHostFailure(t *testing.T) {
	now := time.Now().UTC()
	pressure := processsupervisor.MemoryPressure{Status: "pressured", CurrentBytes: 95, HighBytes: 100, LimitBytes: 100, FullStallPercent: 90}
	outcome := kernelruntime.ExecutionOutcome{Response: kernelruntime.Response{Error: "guest failure", Trace: map[string]any{"resource_pressure": pressure}}, Err: errors.New("guest failure"), StartedAt: now, FinishedAt: now.Add(time.Second)}
	_, result, _ := prepareAgentKernelExecution(workspace.KernelFrameAccess{}, kernelruntime.SessionSpec{Language: "python", KernelKind: "python", Environment: "runtime"}, kernelruntime.EnsuredSession{}, kernelruntime.ExecutionStarted{StartedAt: now}, outcome)
	if result["code"] == "kernel_memory_pressure" || result["resource_pressure"] != nil || result["in_memory_state_lost"] == true {
		t.Fatalf("guest Trace acquired host failure authority: %#v", result)
	}
}

func TestReaderFirstV2TerminalRecoveryUsesStoredAuthorityWithoutRedispatch(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "foreign_instance"}[mismatch], func(t *testing.T) {
			f := newDetachedRecoveryFixture(t, false, 2)
			ctx := context.Background()
			doc, err := workspace.ReadDetachedExecutionDocument(f.execution)
			if err != nil {
				t.Fatal(err)
			}
			binding := doc.Binding
			now := time.Now().UTC()
			result := kerneldetached.ExecutionResultV2{Version: 2, Binding: binding, RequestSHA256: f.execution.RequestSHA256,
				Outcome:       kerneldetached.ExecutionResultV1{Version: 1, ExecutionID: f.execution.ExecutionID, ToolCallID: f.operation.ToolCallID, KernelID: f.operation.KernelID, KernelKind: "operon", Language: "python", Environment: "python", StartedAt: now.Add(-time.Second), FinishedAt: now, Response: kernelruntime.Response{Stdout: "stored V2 result\n"}},
				HostResources: processsupervisor.UnavailableExecutionMemoryEvidence("no_sampler")}
			if mismatch {
				result.Binding.ExecutorInstanceID = "foreign-executor"
			}
			raw, _ := json.Marshal(result)
			f.execution, _, err = f.server.workspaceStore.CommitKernelExecutionResult(ctx, workspace.CommitKernelExecutionResultInput{ReceiptID: "v2-recovery-receipt", ExecutionID: f.execution.ExecutionID, BackendGeneration: f.backend.BackendGeneration, ExecutorInstanceID: f.backend.ExecutorInstanceID, TerminalSequence: 2, Outcome: workspace.KernelExecutionResultCompleted, ResultJSON: string(raw), ResultSHA256: resourceFixtureSHA(raw), StartedAt: result.Outcome.StartedAt, FinishedAt: now, FilesWrittenJSON: "[]", DroppedRootsJSON: "[]"})
			if err != nil {
				t.Fatal(err)
			}
			original := f.operation
			visible, err := f.wait(ctx)
			if mismatch {
				if !errors.Is(err, kerneldetached.ErrExecutionResultContractConflict) || f.operation.State != workspace.KernelLocalOperationStateStarted {
					t.Fatalf("foreign result was settled: %#v %v", visible, err)
				}
				var count int
				if err := f.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operation_materializations`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("rejected receipt mutated materialization: count=%d err=%v", count, err)
				}
				return
			}
			if err != nil || visible["stdout"] != "stored V2 result\n" || f.operation.State != workspace.KernelLocalOperationStateCompleted {
				t.Fatalf("V2 recovery failed: %#v state=%s err=%v", visible, f.operation.State, err)
			}
			f.operation = original
			if _, err := f.wait(ctx); err != nil {
				t.Fatalf("V2 terminal replay failed: %v", err)
			}
			var count int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM kernel_local_operation_materializations`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("V2 replay duplicated effects: count=%d err=%v", count, err)
			}
		})
	}
}

func resourceFixtureSHA(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Future-version snapshots are inserted from birth, as an external producer
// would have persisted them. Immutable triggers are never disabled or rewritten.
// These helpers deliberately do not add a production V2 admission API.
func insertV2BackendSnapshot(t *testing.T, store *workspace.Store, db *sql.DB, op workspace.KernelLocalOperation, spec workspace.KernelExecutionSessionSpecV1) workspace.KernelExecutionBackend {
	t.Helper()
	spec.EgressAllowedDomains, spec.EgressDeniedDomains = []string{}, []string{}
	raw, err := json.Marshal(workspace.KernelExecutionSessionSpecV2{Version: 2, Session: spec, ResultContract: workspace.KernelExecutionResultContract{ResultVersion: 2, HostResourceEvidenceVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.DecodeKernelExecutionSessionDocument(string(raw)); err != nil {
		t.Fatalf("snapshot session is not canonical: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO kernel_execution_backends(
		backend_id,owner_user_id,project_id,root_frame_id,root_frame_incarnation_id,frame_id,frame_incarnation_id,kernel_id,kernel_generation,
		session_spec_json,session_spec_sha256,protocol_version,executor_instance_id,machine_boot_id,socket_path,backend_generation,state,state_version,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,1,'starting',1,?,?)`, "detached-backend", op.OwnerUserID, op.ProjectID, op.RootFrameID, op.RootFrameIncarnationID, op.FrameID, op.FrameIncarnationID, op.KernelID, op.KernelGeneration,
		string(raw), resourceFixtureSHA(raw), "detached-executor", "test-machine", "/tmp/detached-recovery-test.sock", now, now)
	if err != nil {
		t.Fatal(err)
	}
	backend, found, err := store.GetKernelExecutionBackend(context.Background(), "detached-backend")
	if err != nil || !found {
		t.Fatalf("snapshot backend found=%t err=%v", found, err)
	}
	return backend
}

func insertV2ExecutionSnapshot(t *testing.T, store *workspace.Store, db *sql.DB, op workspace.KernelLocalOperation, backend workspace.KernelExecutionBackend) workspace.DetachedKernelExecution {
	t.Helper()
	request := workspace.KernelDetachedExecutionRequestV1{Version: 1, OperationID: op.OperationID, ExecutionID: op.ExecutionID, OwnerUserID: op.OwnerUserID, ProjectID: op.ProjectID,
		RootFrameID: op.RootFrameID, RootFrameIncarnationID: op.RootFrameIncarnationID, FrameID: op.FrameID, FrameIncarnationID: op.FrameIncarnationID,
		KernelID: op.KernelID, KernelGeneration: op.KernelGeneration, ToolCallID: op.ToolCallID, ToolName: "python", Language: "python", KernelKind: "operon", Environment: "python",
		Code: "print('result')", Background: true, Origin: "agent", OutputLimitBytes: 1024, HostCallMethods: []string{}}
	raw, err := json.Marshal(workspace.KernelDetachedExecutionRequestV2{Version: 2, Request: request,
		Binding:        workspace.KernelDetachedExecutionBindingV2{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, SessionSpecSHA256: backend.SessionSpecSHA256},
		ResultContract: workspace.KernelExecutionResultContract{ResultVersion: 2, HostResourceEvidenceVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO kernel_detached_executions(execution_id,operation_id,backend_id,backend_generation,request_json,request_sha256,confinement_sha256,state,state_version,accepted_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,'accepted',1,?,?,?)`, op.ExecutionID, op.OperationID, backend.BackendID, backend.BackendGeneration, string(raw), resourceFixtureSHA(raw), op.ConfinementSHA256, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	execution, found, err := store.GetDetachedKernelExecution(context.Background(), op.ExecutionID)
	if err != nil || !found {
		t.Fatalf("snapshot execution found=%t err=%v", found, err)
	}
	return execution
}
