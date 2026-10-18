package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
	transcriptstore "x-science/internal/persistence/transcript"
	runnermachine "x-science/internal/sessionrunner"
)

var (
	ErrGenerationStopped       = errors.New("generation stopped")
	ErrRuntimeDraining         = errors.New("runtime draining")
	ErrSessionRunAlreadyActive = errors.New("session run is already active")
)

type activeSessionRun struct {
	runnerID          string
	cancel            context.CancelCauseFunc
	done              chan struct{}
	settlement        sync.Mutex
	settled           bool
	draining          atomic.Bool
	transcript        *transcriptRunnerAuthority
	phaseMachine      *runnermachine.Machine
	kernelPeer        kernelMessageBudgets
	chatRun           *sessionRunnerChatRun
	inputBinding      sessionRunInputBinding
	taskRunCancelled  bool
	projectionClaim   sessionstore.RunnerMutationClaim
	contextCompaction *transcriptstore.ContextCompactionRequest
}

type sessionRunInputBinding struct {
	userEventID int64
	runID       string
	instance    string
	verified    bool
}

type activeSessionRunContextKey struct{}

// sessionRunLifetimeContextKey retains the registered execution-unit lifetime
// underneath shorter provider, preparation and request contexts.
type sessionRunLifetimeContextKey struct{}

func (s *Server) activeSessionChatRun(sessionID string) *sessionRunnerChatRun {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	s.sessionRunsMu.Lock()
	defer s.sessionRunsMu.Unlock()
	active := s.sessionRuns[strings.TrimSpace(sessionID)]
	if active == nil {
		return nil
	}
	active.settlement.Lock()
	defer active.settlement.Unlock()
	return active.chatRun
}

func (s *Server) registerActiveSessionRun(parent context.Context, sessionID, runnerID string) (context.Context, *activeSessionRun, func(), error) {
	if parent == nil {
		return nil, nil, nil, errors.New("active session run context is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	runnerID = strings.TrimSpace(runnerID)
	if s == nil || sessionID == "" || runnerID == "" {
		return nil, nil, nil, errors.New("active session run requires a server, session, and runner")
	}
	ctx, cancel := context.WithCancelCause(parent)
	run := &activeSessionRun{runnerID: runnerID, cancel: cancel, done: make(chan struct{})}
	ctx = context.WithValue(ctx, activeSessionRunContextKey{}, run)
	ctx = context.WithValue(ctx, sessionRunLifetimeContextKey{}, ctx)
	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsMu.Unlock()
		cancel(ErrRuntimeDraining)
		return nil, nil, nil, ErrRuntimeDraining
	}
	if existing := s.sessionRuns[sessionID]; existing != nil {
		s.sessionRunsMu.Unlock()
		cancel(errors.New("duplicate active session run"))
		return nil, nil, nil, fmt.Errorf("%w: session %q is already running under %s", ErrSessionRunAlreadyActive, sessionID, existing.runnerID)
	}
	s.sessionRuns[sessionID] = run
	s.sessionRunsMu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			s.sessionRunsMu.Lock()
			if s.sessionRuns[sessionID] == run {
				delete(s.sessionRuns, sessionID)
			}
			s.sessionRunsMu.Unlock()
			cancel(nil)
			close(run.done)
			s.publishContextCompactionDrainedRun(run)
		})
	}
	return ctx, run, cleanup, nil
}

// bindTaskRunSessionInput uses only the snapshot about to cross an execution
// boundary. In particular, a later journal user is not the input of this stage.
func (s *Server) bindTaskRunSessionInput(ctx context.Context, session sessionstore.Session, entries []eventjournal.Entry) error {
	active, _ := ctx.Value(activeSessionRunContextKey{}).(*activeSessionRun)
	if active == nil || s == nil || !strings.HasPrefix(session.ID, "taskrun:") {
		return nil
	}
	binding := taskRunBindingFromEntries(session.ID, entries)
	active.settlement.Lock()
	if cause := context.Cause(ctx); cause != nil {
		active.settlement.Unlock()
		return cause
	}
	if active.settled {
		active.settlement.Unlock()
		return errors.New("session runner input cannot be rebound after settlement")
	}
	active.inputBinding = binding
	claim := active.projectionClaim
	if claim.ClaimToken == "" && active.chatRun != nil {
		claim = sessionstore.RunnerMutationClaim{SessionID: session.ID, RunnerID: active.runnerID, Attempt: active.chatRun.Attempt, ClaimToken: active.chatRun.ClaimToken}
	}
	if claim.ClaimToken == "" {
		claim = sessionstore.RunnerClaimFromSession(session)
	}
	active.settlement.Unlock()
	if binding.userEventID > 0 && s.sessionStore != nil && claim.ClaimToken != "" {
		if err := s.hydrateTaskRunLastUserEventID(ctx, claim); err != nil {
			return err
		}
	}
	if !binding.verified || s.taskRunStore == nil {
		// Legacy missing identities stay executable. Their cancellation response
		// explicitly reports that execution-stop propagation is unconfirmed.
		return nil
	}
	run, found, err := s.taskRunStore.Get(binding.runID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("TaskRun input has no durable graph")
	}
	return cancelTaskRunBindingIfCurrent(ctx, active, binding, run)
}

func taskRunBindingFromEntries(sessionID string, entries []eventjournal.Entry) sessionRunInputBinding {
	binding := sessionRunInputBinding{}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if strings.TrimSpace(stringValue(entry.Message["role"])) != "user" {
			continue
		}
		binding.userEventID = entry.EventID
		binding.runID = strings.TrimSpace(stringValue(entry.Message["taskRun"]))
		runtime := mapValue(entry.Message["runnerRuntime"])
		binding.instance = strings.TrimSpace(stringValue(runtime["taskRunInstanceId"]))
		binding.verified = binding.runID != "" && binding.instance != "" && entry.RunID == binding.runID &&
			stringValue(runtime["runId"]) == binding.runID && stringValue(runtime["sessionId"]) == sessionID &&
			stringValue(runtime["protocol"]) == "session_runner"
		break
	}
	return binding
}

func cancelTaskRunBindingIfCurrent(ctx context.Context, active *activeSessionRun, binding sessionRunInputBinding, run taskruns.Record) error {
	active.settlement.Lock()
	defer active.settlement.Unlock()
	if active.settled || active.inputBinding != binding {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if taskruns.ExecutionInstance(run) != binding.instance || run.Status == "cancelled" {
		active.taskRunCancelled = true
		active.cancel(fmt.Errorf("%w: TaskRun execution cancelled or superseded", ErrGenerationStopped))
		return context.Cause(ctx)
	}
	return nil
}

// stopTaskRunSessionInputs adds no persistence under registry/settlement locks.
// Each pointer's current binding is rechecked after taking its settlement lock.
func (s *Server) stopTaskRunSessionInputs(run taskruns.Record) {
	instance := taskruns.ExecutionInstance(run)
	if instance == "" {
		return
	}
	s.sessionRunsMu.Lock()
	runs := make([]*activeSessionRun, 0, len(s.sessionRuns))
	for _, active := range s.sessionRuns {
		runs = append(runs, active)
	}
	s.sessionRunsMu.Unlock()
	for _, active := range runs {
		active.settlement.Lock()
		binding := active.inputBinding
		if !active.settled && binding.verified && binding.runID == run.RunID && binding.instance == instance {
			active.taskRunCancelled = true
			active.cancel(fmt.Errorf("%w: TaskRun cancelled by request", ErrGenerationStopped))
		}
		active.settlement.Unlock()
	}
}

func (s *Server) hydrateTaskRunLastUserEventID(ctx context.Context, claim sessionstore.RunnerMutationClaim) error {
	for {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		observed, err := s.sessionStore.ValidateRunnerClaim(claim, true)
		if err != nil {
			return err
		}
		if observed.LastUserEventID != 0 {
			return nil
		}
		entries, err := s.eventJournal.ReadAllStrict(claim.SessionID)
		if err != nil {
			return fmt.Errorf("hydrate TaskRun input projection: %w", err)
		}
		lastUserID := int64(0)
		for _, entry := range entries {
			if stringValue(entry.Message["role"]) == "user" {
				lastUserID = entry.EventID
			}
		}
		_, updated, err := s.sessionStore.HydrateLastUserEventID(claim, observed, lastUserID)
		if err != nil || updated {
			return err
		}
	}
}

type taskRunCancellationSettlement struct {
	claim               sessionstore.RunnerMutationClaim
	consumedUserEventID int64
	instance            string
	cancelled           bool
}

// Pre-input parent cancellation can truthfully finish its admitted receipt.
// This snapshot does not bind an execution handle or authorize TaskRun stop.
func (s *Server) taskRunAdmissionSettlement(session sessionstore.Session, claim sessionstore.RunnerMutationClaim) (*taskRunCancellationSettlement, error) {
	if !strings.HasPrefix(session.ID, "taskrun:") || session.LastUserEventID <= 0 {
		return nil, nil
	}
	entries, err := s.eventJournal.ReadAfter(session.ID, session.LastUserEventID-1, 1)
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 || entries[0].EventID != session.LastUserEventID {
		return nil, errors.New("TaskRun admission input receipt is missing")
	}
	binding := taskRunBindingFromEntries(session.ID, entries)
	if binding.userEventID <= 0 {
		return nil, errors.New("TaskRun admission receipt is not user input")
	}
	instance := ""
	if binding.verified {
		instance = binding.instance
	}
	return &taskRunCancellationSettlement{claim: claim, consumedUserEventID: binding.userEventID, instance: instance}, nil
}

// Called by the owning runner while settlement is locked, never from request
// input. A user-provided service-operation map cannot opt into this behavior.
func taskRunCancellationSettlementLocked(active *activeSessionRun, claim sessionstore.RunnerMutationClaim) *taskRunCancellationSettlement {
	if active == nil || active.inputBinding.userEventID <= 0 {
		return nil
	}
	instance := ""
	if active.inputBinding.verified {
		instance = active.inputBinding.instance
	}
	return &taskRunCancellationSettlement{claim: claim, consumedUserEventID: active.inputBinding.userEventID, instance: instance, cancelled: active.taskRunCancelled}
}

func (s *Server) hasActiveSessionRun(sessionID string) bool {
	if s == nil {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	s.sessionRunsMu.Lock()
	defer s.sessionRunsMu.Unlock()
	return s.sessionRuns[sessionID] != nil
}

func (s *Server) isDraining() bool {
	if s == nil {
		return false
	}
	s.sessionRunsMu.Lock()
	defer s.sessionRunsMu.Unlock()
	return s.sessionRunsDraining
}

func (s *Server) sessionRunActivity() (active int, draining bool) {
	active, _, draining = s.runtimeActivity()
	return active, draining
}

func (s *Server) activeKernelExecutionCount() int {
	if s == nil {
		return 0
	}
	inMemory := 0
	if s.kernelManager != nil {
		inMemory = s.kernelManager.ActiveExecutionCount()
	}
	if s.workspaceStore == nil {
		return inMemory
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	durable, err := s.workspaceStore.CountActiveDetachedKernelExecutions(ctx)
	if err != nil || durable <= inMemory {
		return inMemory
	}
	// The same execution normally exists in both projections. max(), rather
	// than sum(), avoids double counting while still preserving restart and
	// pending-recovery liveness that has no current in-memory handle.
	return durable
}

func (s *Server) activeDurableRunnerAttemptCount() int {
	if s == nil || s.transcriptStore == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	count, err := s.transcriptStore.CountActiveRunnerAttempts(ctx)
	if err != nil {
		// A failed liveness read is not proof that deployment is safe.
		return 1
	}
	return count
}

func (s *Server) activeKernelOperationCount() int {
	if s == nil {
		return 0
	}
	physical := s.kernelManager.ActiveManagedEnvironmentOperationCount()
	if s.workspaceStore == nil {
		return physical
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	count, err := s.workspaceStore.CountActiveKernelLocalOperations(ctx)
	if err != nil {
		// Preserve the running backend when durable work cannot be classified.
		return max(1, physical)
	}
	return max(count, physical)
}

// TryDrainIdle atomically enters draining mode only when no runner is active.
// It is used by source/deployment supervisors to activate a verified binary
// without racing a new task admission or interrupting an in-flight task.
func (s *Server) TryDrainIdle() bool {
	if s == nil {
		return false
	}
	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsMu.Unlock()
		return true
	}
	if s.sessionRunsDrainEvaluating {
		s.sessionRunsMu.Unlock()
		return false
	}
	if s.hasActiveRuntimeOwnersLocked() {
		s.sessionRunsMu.Unlock()
		return false
	}
	s.sessionRunsDrainEvaluating = true
	s.sessionRunsMu.Unlock()

	clearDrainEvaluating := func() {
		s.sessionRunsMu.Lock()
		s.sessionRunsDrainEvaluating = false
		s.sessionRunsMu.Unlock()
	}

	if wait := s.tryDrainIdleDurableWait; wait != nil {
		wait()
	}
	// Durable liveness reads can take hundreds of milliseconds each. Do not hold
	// sessionRunsMu across them or /health and deployment supervisors stall.
	if s.activeDurableRunnerAttemptCount() != 0 ||
		s.activeKernelExecutionCount() != 0 ||
		s.activeKernelOperationCount() != 0 {
		clearDrainEvaluating()
		return false
	}

	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsDrainEvaluating = false
		s.sessionRunsMu.Unlock()
		return true
	}
	if s.hasActiveRuntimeOwnersLocked() {
		s.sessionRunsDrainEvaluating = false
		s.sessionRunsMu.Unlock()
		return false
	}
	s.sessionRunsMu.Unlock()

	if s.activeDurableRunnerAttemptCount() != 0 ||
		s.activeKernelExecutionCount() != 0 ||
		s.activeKernelOperationCount() != 0 {
		clearDrainEvaluating()
		return false
	}

	s.sessionRunsMu.Lock()
	if s.sessionRunsDraining {
		s.sessionRunsDrainEvaluating = false
		s.sessionRunsMu.Unlock()
		return true
	}
	if s.hasActiveRuntimeOwnersLocked() {
		s.sessionRunsDrainEvaluating = false
		s.sessionRunsMu.Unlock()
		return false
	}
	// A durable local operation can outlive the runner cycle that submitted it.
	// Treat that execution as active runtime work as well; otherwise a verified
	// source update can replace the backend while the kernel is still settling,
	// leaving replay with a start checkpoint but no live kernel to complete it.
	s.sessionRunsDraining = true
	s.sessionRunsDrainEvaluating = false
	s.sessionRunsMu.Unlock()
	return true
}

// IsDraining reports whether the server has stopped accepting runtime work.
// Runtime supervisors use this to distinguish intentional shutdown from a
// component failure without cancelling active runner contexts prematurely.
func (s *Server) IsDraining() bool {
	return s.isDraining()
}

func (s *Server) stopActiveSessionRun(sessionID, reason string) (bool, string) {
	if s == nil {
		return false, ""
	}
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "stopped by user"
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	s.sessionRunsMu.Lock()
	run := s.sessionRuns[sessionID]
	s.sessionRunsMu.Unlock()
	if run == nil {
		return false, ""
	}
	run.settlement.Lock()
	defer run.settlement.Unlock()
	if run.settled {
		return false, run.runnerID
	}
	run.cancel(fmt.Errorf("%w: %s", ErrGenerationStopped, reason))
	return true, run.runnerID
}

func (s *Server) commitSessionRunCancellation(sessionIDs []string, commit func(map[string]*transcriptRunnerAuthority) error) error {
	if commit == nil {
		return errors.New("session cancellation commit is required")
	}
	return s.commitScopedSessionRunCancellation(sessionIDs, func(authorities map[string]*transcriptRunnerAuthority) ([]string, error) {
		if err := commit(authorities); err != nil {
			return nil, err
		}
		return sessionIDs, nil
	})
}

// A preaccept cancellation can commit a tombstone without cancelling any run.
// Signal only IDs actually cancelled by the committed durable transaction.
func (s *Server) commitScopedSessionRunCancellation(sessionIDs []string, commit func(map[string]*transcriptRunnerAuthority) ([]string, error)) error {
	if s == nil || commit == nil {
		return errors.New("session cancellation commit is required")
	}
	ids := normalizedSessionRunIDs(sessionIDs)
	s.sessionRunsMu.Lock()
	runs := make(map[string]*activeSessionRun, len(ids))
	for _, sessionID := range ids {
		if run := s.sessionRuns[sessionID]; run != nil {
			run.settlement.Lock()
			runs[sessionID] = run
		}
	}
	authorities := make(map[string]*transcriptRunnerAuthority, len(runs))
	for sessionID, run := range runs {
		authorities[sessionID] = run.transcript
	}
	cancelledIDs, err := commit(authorities)
	if err == nil {
		for _, id := range normalizedSessionRunIDs(cancelledIDs) {
			if run := runs[id]; run != nil && !run.settled {
				run.cancel(fmt.Errorf("%w: cancelled by durable frame authority", ErrGenerationStopped))
			}
		}
	}
	for index := len(ids) - 1; index >= 0; index-- {
		if run := runs[ids[index]]; run != nil {
			run.settlement.Unlock()
		}
	}
	s.sessionRunsMu.Unlock()
	return err
}

func normalizedSessionRunIDs(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *Server) drainAllActiveSessionRuns(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.sessionRunsMu.Lock()
	s.sessionRunsDraining = true
	runs := make([]*activeSessionRun, 0, len(s.sessionRuns))
	for _, run := range s.sessionRuns {
		runs = append(runs, run)
	}
	s.sessionRunsMu.Unlock()
	for _, run := range runs {
		// Publish drain intent before competing for settlement. Terminal writers
		// that reach the mutex first must still observe that drain won globally.
		if run != nil {
			run.draining.Store(true)
		}
	}
	pending := append([]*activeSessionRun(nil), runs...)
	for len(pending) > 0 {
		next := make([]*activeSessionRun, 0, len(pending))
		for _, run := range pending {
			if run == nil {
				continue
			}
			// Settlement is the linearization boundary between a durable terminal
			// result and a resumable infrastructure interruption. Taking the same
			// lock as terminal writers guarantees that either the terminal commit
			// won first or the runner observes ErrRuntimeDraining before committing.
			if !run.settlement.TryLock() {
				next = append(next, run)
				continue
			}
			if !run.settled && run.cancel != nil {
				run.cancel(ErrRuntimeDraining)
			}
			run.settlement.Unlock()
		}
		pending = next
		if len(pending) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	for _, run := range runs {
		if run == nil || run.done == nil {
			continue
		}
		select {
		case <-run.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Drain stops accepting work in active in-process runs and waits until each
// one has persisted a resumable interruption. It is intentionally distinct
// from user cancellation, which is a terminal business action.
func (s *Server) Drain(ctx context.Context) error {
	if ctx == nil {
		return errors.New("server drain context is required")
	}
	return s.drainAllActiveSessionRuns(ctx)
}
