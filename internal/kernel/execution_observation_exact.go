package kernel

import (
	"fmt"
	"time"
)

// LocalExecutionTarget is a snapshot of one existing local registration, not
// permission to create a process or to select a newer generation at submission.
type LocalExecutionTarget struct {
	OwnerID, ProjectID, FrameID, RootFrameID    string
	FrameIncarnationID, RootFrameIncarnationID  string
	KernelID, KernelKind, Language, Environment string
	Generation                                  uint64
}

func (m *Manager) LocalExecutionTarget(kernelID string) (LocalExecutionTarget, bool) {
	if m == nil {
		return LocalExecutionTarget{}, false
	}
	worker := m.workerByID(kernelID)
	if worker == nil {
		return LocalExecutionTarget{}, false
	}
	state := lifecycleWorkerState(worker)
	if state == nil {
		return LocalExecutionTarget{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closing || state.spec.Fresh || state.generation == 0 {
		return LocalExecutionTarget{}, false
	}
	select {
	case <-worker.done:
		return LocalExecutionTarget{}, false
	default:
	}
	spec := state.spec
	return LocalExecutionTarget{
		OwnerID: spec.OwnerID, ProjectID: spec.ProjectID, FrameID: spec.FrameID, RootFrameID: spec.RootFrameID,
		FrameIncarnationID: spec.FrameIncarnationID, RootFrameIncarnationID: spec.RootFrameIncarnationID,
		KernelID: spec.KernelID, KernelKind: spec.KernelKind, Language: spec.Language, Environment: spec.Environment,
		Generation: state.generation,
	}, true
}

// ExactExecutionObservation does not consume Started/Done or expose mutable
// worker pointers. Running is a Manager dispatch state; the public observer
// must also require the actual worker-start receipt before claiming execution.
type ExactExecutionObservation struct {
	Status    string
	SampledAt time.Time
}

func (m *Manager) ObserveExecution(expected ExecutionIdentity) (ExactExecutionObservation, bool) {
	execution := m.exactExecution(expected)
	if execution == nil {
		return ExactExecutionObservation{}, false
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	lifecycleRegistry.mu.Lock()
	current := lifecycleRegistry.executions[execution.key] == execution
	lifecycleRegistry.mu.Unlock()
	if !current || execution.identity != expected {
		return ExactExecutionObservation{}, false
	}
	if execution.status != "persisting" {
		select {
		case <-execution.worker.done:
			return ExactExecutionObservation{}, false
		default:
		}
	}
	switch execution.status {
	case "queued", "running", "persisting":
		return ExactExecutionObservation{Status: execution.status, SampledAt: time.Now().UTC()}, true
	default:
		return ExactExecutionObservation{}, false
	}
}

func (m *Manager) exactExecution(expected ExecutionIdentity) *managedExecution {
	if m == nil || expected.ExecutionID == "" || expected.OwnerID == "" || expected.ProjectID == "" ||
		expected.FrameID == "" || expected.RootFrameID == "" || expected.FrameIncarnationID == "" ||
		expected.RootFrameIncarnationID == "" || expected.KernelID == "" || expected.KernelGeneration == 0 {
		return nil
	}
	key := executionKey{manager: fmt.Sprintf("%p", m), frameID: expected.FrameID, execID: expected.ExecutionID}
	lifecycleRegistry.mu.Lock()
	execution := lifecycleRegistry.executions[key]
	lifecycleRegistry.mu.Unlock()
	if execution == nil || execution.identity != expected {
		return nil
	}
	return execution
}

// InterruptExecution uses the same state transition as legacy interruption,
// but retains the original pointer and rechecks the complete frozen identity.
// It never observes by identity then looks up a weaker frame/exec key again.
func (m *Manager) InterruptExecution(expected ExecutionIdentity) InterruptResult {
	return interruptManagedExecution(m.exactExecution(expected), &expected)
}

func interruptManagedExecution(execution *managedExecution, expected *ExecutionIdentity) InterruptResult {
	if execution == nil {
		return InterruptResult{Reason: "no such terminal cell"}
	}
	execution.mu.Lock()
	if expected != nil {
		lifecycleRegistry.mu.Lock()
		current := lifecycleRegistry.executions[execution.key] == execution && execution.identity == *expected
		lifecycleRegistry.mu.Unlock()
		if !current {
			execution.mu.Unlock()
			return InterruptResult{Reason: "execution identity changed"}
		}
	}
	switch execution.status {
	case "queued":
		execution.status = "dequeued"
		execution.mu.Unlock()
		// File snapshots and completion callbacks run outside every registry
		// lock. The exact pointer above cannot target a later registration.
		execution.finish(ExecutionOutcome{Dequeued: true})
		return InterruptResult{Dequeued: true, Reason: "cell had not started \u2014 dequeued"}
	case "running":
		execution.mu.Unlock()
		select {
		case execution.interrupt <- struct{}{}:
		default:
		}
		return InterruptResult{Interrupted: true, Via: "sigint"}
	default:
		execution.mu.Unlock()
		return InterruptResult{Reason: "no such terminal cell"}
	}
}
