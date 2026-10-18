package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

func TestManualNotebookRejectedAdmissionCannotSettleAnotherFrozenReceipt(t *testing.T) {
	for _, conflict := range []string{"binding", "request"} {
		t.Run(conflict, func(t *testing.T) {
			store, manager, _, access := newManualNotebookHTTPRuntime(t)
			request := workspace.ManualKernelRequest{RequestID: uuid.NewString(), Source: "print('must not execute')", Language: "python", Environment: "python", TimeoutMillis: 1000}
			id := workspace.ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID)
			gate := make(chan error, 1)
			handle, err := manager.Submit(kernelruntime.SubmitRequest{
				OwnerID: access.UserID, ProjectID: access.Frame.ProjectID, FrameID: access.Frame.ID,
				FrameIncarnationID: access.Frame.IncarnationID, RootFrameIncarnationID: access.RootFrameIncarnationID,
				Language: request.Language, Environment: request.Environment, ExecID: id, ToolUseID: "user-" + id,
				Code: request.Source, Origin: "user", Timeout: time.Second, StartAuthorization: gate, PersistenceRequired: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			identity := handle.Identity()
			ours := workspace.AdmitManualKernelExecutionInput{Request: request, Binding: workspace.ManualKernelBinding{
				ExecutionID: id, OwnerID: identity.OwnerID, ProjectID: identity.ProjectID,
				RootFrameID: identity.RootFrameID, RootIncarnationID: identity.RootFrameIncarnationID,
				FrameID: identity.FrameID, FrameIncarnationID: identity.FrameIncarnationID,
				KernelID: identity.KernelID, KernelGeneration: identity.KernelGeneration, KernelKind: identity.KernelKind, ServerBootID: "our-boot",
			}}
			other := ours
			if conflict == "binding" {
				other.Binding.ServerBootID = "other-boot"
			} else {
				other.Request.Source = "print('previously accepted body')"
			}
			original, created, err := store.AdmitManualKernelExecution(context.Background(), other)
			if err != nil || !created {
				t.Fatalf("original admission created=%t err=%v", created, err)
			}
			rejected, _, err := store.AdmitManualKernelExecution(context.Background(), ours)
			if !errors.Is(err, workspace.ErrManualKernelExecutionConflict) {
				t.Fatalf("conflicting admission error=%v", err)
			}
			gate <- err
			close(gate)
			var outcome kernelruntime.ExecutionOutcome
			select {
			case outcome = <-handle.Done():
			case <-time.After(time.Second):
				t.Fatal("rejected gate did not finish")
			}
			if !outcome.Dequeued || !outcome.StartedAt.IsZero() {
				t.Fatalf("rejected admission executed: %#v", outcome)
			}
			server := &Server{workspaceStore: store}
			server.settleManualNotebookExecution(context.Background(), access, rejected, handle, manualNotebookCompletion(rejected, nil, outcome), true)
			current, found, err := store.GetManualKernelExecution(context.Background(), access, id)
			if err != nil || !found || current.State != "queued" || current.StateVersion != original.StateVersion || current.ManualKernelBinding != original.ManualKernelBinding || current.Request != original.Request {
				t.Fatalf("conflicting handle mutated original receipt: %#v err=%v", current, err)
			}
			if manager.ActiveExecutionCount() != 0 {
				t.Fatal("rejected losing registration was not released")
			}
			var count int
			if err := store.WithTransaction(context.Background(), func(tx *sql.Tx) error {
				return tx.QueryRow(`SELECT COUNT(*) FROM workspace_outbox WHERE event_type='execution_cell_update'`).Scan(&count)
			}); err != nil || count != 0 {
				t.Fatalf("conflicting loser created delivery side effects count=%d err=%v", count, err)
			}
		})
	}
}
