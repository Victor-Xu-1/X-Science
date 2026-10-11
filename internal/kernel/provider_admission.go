package kernel

import (
	"context"
	"errors"
	"time"
)

// ProviderSessionAdmission guards the short physical-start/registration or
// reuse boundary. Handshakes and user executions must remain outside it.
type ProviderSessionAdmission func(register func() error) error

func providerSessionAdmission(admissions []ProviderSessionAdmission) (ProviderSessionAdmission, error) {
	if len(admissions) == 0 {
		return func(register func() error) error { return register() }, nil
	}
	if len(admissions) != 1 || admissions[0] == nil {
		return nil, errors.New("provider session requires one valid admission guard")
	}
	return admissions[0], nil
}

func providerWorkerMatchesSession(worker *Worker, spec SessionSpec) bool {
	state := lifecycleWorkerState(worker)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return !state.closing && sameSessionSpec(state.spec, spec)
}

// registerProviderWorker runs synchronously at actual creation, before the
// manager releases its worker lock or starts any lifecycle consumers. Grant
// revocation can therefore never capture a launched provider with no owner.
func registerProviderWorker(worker *Worker, spec SessionSpec) {
	state := &workerLifecycle{
		spec: spec, generation: lifecycleGeneration.Add(1), lastUsed: time.Now().UTC(), workingDir: spec.WorkspaceDir,
	}
	lifecycleRegistry.mu.Lock()
	lifecycleRegistry.workers[worker] = state
	lifecycleRegistry.mu.Unlock()
	go releaseLifecycleWorker(worker, worker.done)
}

// A failed startup owns this exact newly created worker, not an existing live
// session. Bound its cleanup without closing any sibling or replacement.
func (m *Manager) closeProviderStartupWorker(worker *Worker) {
	ctx, cancel := context.WithTimeout(context.Background(), m.config.ShutdownTimeout)
	defer cancel()
	_ = worker.Close(ctx)
}
