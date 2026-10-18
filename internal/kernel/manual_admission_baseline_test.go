package kernel

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type manualAdmissionProtocolWriter struct{ writes atomic.Int32 }

func (writer *manualAdmissionProtocolWriter) Write(data []byte) (int, error) {
	writer.writes.Add(1)
	return len(data), nil
}
func (*manualAdmissionProtocolWriter) Close() error { return nil }

// Like TestManagedExecutionStartAuthorizationRejectsBeforeWorkerIO, this
// fixture has no process. Submit, registry, gate, cancellation and finish are
// production paths; the protocol writer records any unintended worker IO.
func manualAdmissionManagerFixture(t *testing.T) (*Manager, *manualAdmissionProtocolWriter, SubmitRequest) {
	t.Helper()
	manager := &Manager{workers: map[string]*Worker{}}
	writer := &manualAdmissionProtocolWriter{}
	worker := &Worker{id: "manual-kernel", manager: manager, workspaceDir: t.TempDir(), stdin: writer}
	manager.workers[worker.id] = worker
	state := &workerLifecycle{generation: 7, spec: SessionSpec{
		KernelID: worker.id, FrameID: "manual-frame", RootFrameID: "manual-frame", KernelKind: "analysis",
		OwnerID: "owner", ProjectID: "project", FrameIncarnationID: "frame-incarnation", RootFrameIncarnationID: "frame-incarnation",
		Language: "python", Environment: "python",
	}}
	lifecycleRegistry.mu.Lock()
	lifecycleRegistry.workers[worker] = state
	lifecycleRegistry.mu.Unlock()
	t.Cleanup(func() {
		lifecycleRegistry.mu.Lock()
		delete(lifecycleRegistry.workers, worker)
		for key, execution := range lifecycleRegistry.executions {
			if execution.worker == worker {
				delete(lifecycleRegistry.executions, key)
			}
		}
		lifecycleRegistry.mu.Unlock()
	})
	return manager, writer, SubmitRequest{
		KernelID: worker.id, ExpectedGeneration: 7, FrameID: "manual-frame",
		OwnerID: "owner", ProjectID: "project", FrameIncarnationID: "frame-incarnation", RootFrameIncarnationID: "frame-incarnation",
		Language: "python", Environment: "python", ExecID: t.Name(), ToolUseID: "user-test", Origin: "user", Code: "print('must not run')",
	}
}

func TestManualKernelExistingGateRejectsAndCancelsWithoutWorkerIO(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "queued cancel"}[cancel], func(t *testing.T) {
			manager, writer, request := manualAdmissionManagerFixture(t)
			gate := make(chan error, 1)
			request.StartAuthorization = gate
			handle, err := manager.Submit(request)
			if err != nil {
				t.Fatal(err)
			}
			if manager.ActiveExecutionCount() != 1 || writer.writes.Load() != 0 {
				t.Fatal("registration was not a nonexecuting reservation")
			}
			denied := errors.New("durable admission denied")
			if cancel {
				if result := manager.InterruptSession(request.FrameID, request.FrameIncarnationID, request.RootFrameIncarnationID, request.ExecID); !result.Dequeued || result.Interrupted {
					t.Fatalf("queued cancellation=%#v", result)
				}
				gate <- nil // Late authorization cannot revive the dequeued item.
			} else {
				gate <- denied
			}
			close(gate)
			outcome := waitForLifecycleOutcome(t, handle)
			if !outcome.Dequeued || !outcome.StartedAt.IsZero() || !cancel && !errors.Is(outcome.Err, denied) {
				t.Fatalf("unexpected prestart outcome=%#v", outcome)
			}
			if writer.writes.Load() != 0 || manager.ActiveExecutionCount() != 0 {
				t.Fatal("rejected/cancelled reservation executed or leaked")
			}
		})
	}
}

func TestManualKernelLegacyForegroundReleasesBeforeObserverPersistence(t *testing.T) {
	manager, _, request := manualAdmissionManagerFixture(t)
	gate := make(chan error, 1)
	request.StartAuthorization = gate
	handle, err := manager.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the existing production terminal routine with a controlled
	// outcome. This establishes the legacy gap, not actual execution evidence.
	handle.execution.finish(ExecutionOutcome{Response: Response{ID: request.ExecID, Stdout: "fixture result"}, StartedAt: time.Now().UTC()})
	_ = waitForLifecycleOutcome(t, handle)
	close(gate)
	if manager.ActiveExecutionCount() != 0 {
		t.Fatal("legacy foreground behavior changed; an explicit persistence option must remain opt-in")
	}
	t.Log("baseline: foreground registry ownership ends before an observer has committed any result")
}
