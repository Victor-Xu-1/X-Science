package kernel

import (
	"errors"
	"testing"
)

func TestManualKernelExactObservationAndCancellationRequireWholeIdentity(t *testing.T) {
	manager, writer, request := manualAdmissionManagerFixture(t)
	gate := make(chan error, 1)
	request.StartAuthorization, request.PersistenceRequired = gate, true
	handle, err := manager.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	expected := handle.Identity()
	for _, mutate := range []func(*ExecutionIdentity){
		func(v *ExecutionIdentity) { v.OwnerID = "other-owner" },
		func(v *ExecutionIdentity) { v.ProjectID = "other-project" },
		func(v *ExecutionIdentity) { v.RootFrameID = "other-root" },
		func(v *ExecutionIdentity) { v.RootFrameIncarnationID = "new-root-incarnation" },
		func(v *ExecutionIdentity) { v.FrameIncarnationID = "new-frame-incarnation" },
		func(v *ExecutionIdentity) { v.KernelGeneration++ },
		func(v *ExecutionIdentity) { v.Source = "changed source" },
		func(v *ExecutionIdentity) { v.Timeout++ },
	} {
		changed := expected
		mutate(&changed)
		if _, found := manager.ObserveExecution(changed); found {
			t.Fatalf("partial identity observed: %#v", changed)
		}
		result := manager.InterruptExecution(changed)
		if result.Interrupted || result.Dequeued {
			t.Fatalf("partial identity cancelled: %#v", changed)
		}
	}
	observation, found := manager.ObserveExecution(expected)
	if !found || observation.Status != "queued" || observation.SampledAt.IsZero() {
		t.Fatalf("exact queued observation=%#v found=%t", observation, found)
	}
	if result := manager.InterruptExecution(expected); !result.Dequeued || result.Interrupted {
		t.Fatalf("exact queued cancellation=%#v", result)
	}
	close(gate)
	outcome := waitForLifecycleOutcome(t, handle)
	if !outcome.Dequeued || writer.writes.Load() != 0 {
		t.Fatalf("queued cancellation executed code: %#v writes=%d", outcome, writer.writes.Load())
	}
	observation, found = manager.ObserveExecution(expected)
	if !found || observation.Status != "persisting" {
		t.Fatalf("pending persistence disappeared: %#v found=%t", observation, found)
	}
	handle.AcknowledgePersistence()
	if _, found := manager.ObserveExecution(expected); found {
		t.Fatal("ACKed registration remained observable")
	}
}

func TestManualKernelExactCancellationRejectsReplacedRegistrationPointer(t *testing.T) {
	manager, writer, request := manualAdmissionManagerFixture(t)
	oldGate := make(chan error, 1)
	request.StartAuthorization, request.PersistenceRequired = oldGate, true
	old, err := manager.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := old.Identity()
	if result := manager.InterruptExecution(oldIdentity); !result.Dequeued {
		t.Fatalf("initial dequeue=%#v", result)
	}
	close(oldGate)
	_ = waitForLifecycleOutcome(t, old)
	old.AcknowledgePersistence()
	state := old.execution.state
	state.mu.Lock()
	state.generation++
	request.ExpectedGeneration = state.generation
	state.mu.Unlock()
	newGate := make(chan error, 1)
	request.StartAuthorization, request.Code = newGate, "print('replacement must stay queued')"
	replacement, err := manager.Submit(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		newGate <- errors.New("test cleanup")
		close(newGate)
		_ = waitForLifecycleOutcome(t, replacement)
		replacement.AcknowledgePersistence()
	})
	// The old pointer may already have been captured before registry removal.
	// Both the public lookup and final helper must reject that stale ownership.
	for _, result := range []InterruptResult{
		manager.InterruptExecution(oldIdentity), interruptManagedExecution(old.execution, &oldIdentity),
	} {
		if result.Interrupted || result.Dequeued {
			t.Fatalf("old registration cancelled replacement: %#v", result)
		}
	}
	if _, found := manager.ObserveExecution(oldIdentity); found {
		t.Fatal("old identity observed replacement")
	}
	observed, found := manager.ObserveExecution(replacement.Identity())
	if !found || observed.Status != "queued" || writer.writes.Load() != 0 {
		t.Fatalf("replacement state=%#v found=%t writes=%d", observed, found, writer.writes.Load())
	}
}

func TestManualKernelLocalTargetSnapshotsGenerationWithoutCreatingWorker(t *testing.T) {
	manager, writer, request := manualAdmissionManagerFixture(t)
	target, found := manager.LocalExecutionTarget(request.KernelID)
	if !found || target.Generation != 7 || target.OwnerID != request.OwnerID || target.FrameIncarnationID != request.FrameIncarnationID || target.RootFrameIncarnationID != request.RootFrameIncarnationID {
		t.Fatalf("local target=%#v found=%t", target, found)
	}
	target.Generation = 99
	again, _ := manager.LocalExecutionTarget(request.KernelID)
	if again.Generation != 7 || writer.writes.Load() != 0 || manager.ActiveExecutionCount() != 0 {
		t.Fatal("target query mutated or executed the registration")
	}
	if _, found := manager.LocalExecutionTarget("missing-kernel"); found {
		t.Fatal("target query invented a worker")
	}
	state := lifecycleWorkerState(manager.workerByID(request.KernelID))
	state.mu.Lock()
	state.closing = true
	state.mu.Unlock()
	if _, found := manager.LocalExecutionTarget(request.KernelID); found {
		t.Fatal("closing worker advertised a submit target")
	}
}
