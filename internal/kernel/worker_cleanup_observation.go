package kernel

import (
	"context"
	"errors"
)

// WorkerProcessCleanupError reports an observable failure, not proof that a
// process tree has stopped. Its physical owner must remain reserved until the
// original termination authority establishes settlement.
type WorkerProcessCleanupError struct {
	State string
	Cause error
}

func (failure *WorkerProcessCleanupError) Error() string {
	if failure != nil && failure.State == "cleanup_incomplete" {
		return "worker process cleanup is incomplete; existing resources remain reserved and automatic replay is unsafe"
	}
	return "worker process cleanup outcome is unknown; existing resources remain reserved and automatic replay is unsafe"
}

func (failure *WorkerProcessCleanupError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type workerCleanupReporterKey struct{}
type workerCleanupReporter func(error)

func withWorkerCleanupReporter(ctx context.Context, reporter workerCleanupReporter) context.Context {
	return context.WithValue(ctx, workerCleanupReporterKey{}, reporter)
}

// reportWorkerCleanup returns whether an existing operation owns the resource
// reservation. A caller with such an owner must not unwind its physical call
// stack merely because an observer has received this error.
func reportWorkerCleanup(ctx context.Context, err error) bool {
	if ctx == nil || err == nil {
		return false
	}
	reporter, _ := ctx.Value(workerCleanupReporterKey{}).(workerCleanupReporter)
	if reporter == nil {
		return false
	}
	var cleanup *WorkerProcessCleanupError
	if !errors.As(err, &cleanup) {
		err = &WorkerProcessCleanupError{State: "cleanup_unknown", Cause: err}
	}
	reporter(err)
	return true
}

// ActiveManagedEnvironmentOperationCount includes physical owners whose
// observers already received cleanup_unknown. A failed observation does not
// make a backend safe to replace while its original cleanup is still pending.
func (m *Manager) ActiveManagedEnvironmentOperationCount() int {
	if m == nil {
		return 0
	}
	m.managedEnvironmentMu.Lock()
	defer m.managedEnvironmentMu.Unlock()
	return len(m.managedEnvironmentOperations)
}

func (m *Manager) managedEnvironmentWaiterError(operation *managedEnvironmentOperation, cause error) error {
	if m == nil || operation == nil {
		return cause
	}
	m.managedEnvironmentMu.Lock()
	cleanupErr := operation.cleanupErr
	m.managedEnvironmentMu.Unlock()
	// Cancellation and observation can become ready together. Preserve the
	// latched uncertainty even if select chose the cancellation/done branch.
	return errors.Join(cause, cleanupErr)
}
