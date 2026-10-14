//go:build linux

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	"x-science/internal/kernel/detached"
	workspace "x-science/internal/persistence/workspace"
)

// Capacity observation and process launch are controlled. Reservation admission,
// backend readiness, actual Python, Engine, approval and tool checkpoints are real.
type resourceContinuationLauncher struct {
	store     *workspace.Store
	manager   *kernelruntime.Manager
	ctx       context.Context
	spool     string
	available atomic.Bool
	launched  atomic.Bool
	observed  chan struct{}
	once      sync.Once
	done      chan error
}

func (l *resourceContinuationLauncher) Launch(request detached.ExecutorLaunchRequest) error {
	backend, found, err := l.store.GetKernelExecutionBackend(l.ctx, request.BackendID)
	if err != nil || !found || backend.BackendGeneration != request.BackendGeneration {
		return errors.New("controlled launch lost the requested backend generation")
	}
	capacity := workspace.KernelResourceCapacity{DefaultBytes: 1 << 30, MinimumBytes: 768 << 20}
	if l.available.Load() {
		capacity.AvailableBytes = 2 << 30
	}
	_, admitted, err := l.store.AdmitKernelResources(l.ctx, backend, 0, capacity)
	if err != nil {
		return err
	}
	l.once.Do(func() { close(l.observed) })
	if !admitted {
		return &detached.ExecutorResourceUnavailableError{BackendID: backend.BackendID, Generation: backend.BackendGeneration, RequiredBytes: capacity.MinimumBytes}
	}
	if !l.launched.CompareAndSwap(false, true) {
		return errors.New("duplicate physical launch")
	}
	executor := &detached.Executor{Store: l.store, Manager: l.manager, BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, SocketPath: backend.SocketPath, ResultSpoolDir: l.spool, HeartbeatInterval: time.Second, IdleTimeout: time.Minute}
	go func() { l.done <- executor.Run(l.ctx) }()
	return nil
}

func (l *resourceContinuationLauncher) Observe(context.Context, string, int64) (detached.ExecutorLaunchState, error) {
	if l.launched.Load() {
		return detached.ExecutorLaunchAlive, nil
	}
	return detached.ExecutorLaunchExited, nil
}

type resourceContinuationModel struct{ requests atomic.Int32 }

func (m *resourceContinuationModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	if m.requests.Add(1) == 1 {
		return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "provider-resource-call", Name: "python", Arguments: json.RawMessage(`{"environment":"python","code":"with open('resource-continuation-count.txt', 'a') as output:\n    output.write('x')\nprint(17)","human_description":"Run the controlled calculation"}`)}}}}, nil
	}
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: "The controlled calculation finished."}}, nil
}

type resourceContinuationFixture struct {
	f        *agentSaveArtifactsFixture
	backend  *detached.Backend
	launcher *resourceContinuationLauncher
	run      *sessionRunnerChatRun
	model    *resourceContinuationModel
	engine   agentruntime.Engine
	schemas  []agentruntime.ToolSchema
	callIDs  chan string
	failed   atomic.Int32
	results  sync.Map
}

func newResourceContinuationFixture(t *testing.T) *resourceContinuationFixture {
	t.Helper()
	manager := newServerKernelTestManagerWithPythonEnvironment(t, "python")
	if !manager.ManagedPythonCapabilityAvailable() || !manager.RuntimeReady("python", "python") {
		t.Fatal("controlled continuation requires the real Python runtime")
	}
	f := newAgentSaveArtifactsFixtureWithKernelManager(t, manager)
	seedPermissionTestAgent(t, f.store, f.stream.OwnerID)
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.FrameID, workspace.FrameRuntimeMetadata{FrameID: f.stream.FrameID, ContextData: map[string]any{"web_assistant": map[string]any{"id": "xscience:operon", "conversation_overrides": map[string]any{"permission": "allow"}}}}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	socketRoot, err := detached.DefaultSocketRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executorCtx, stopExecutor := context.WithCancel(context.Background())
	launcher := &resourceContinuationLauncher{store: f.store, manager: manager, ctx: executorCtx, spool: filepath.Join(home, "spool"), observed: make(chan struct{}), done: make(chan error, 1)}
	backend := &detached.Backend{Store: f.store, Executable: executable, HomeDir: home, SocketRoot: socketRoot, LogDir: filepath.Join(home, "logs"), Launcher: launcher, StartTimeout: 5 * time.Second}
	f.server.kernelExecutionBackend = backend
	t.Cleanup(func() {
		stopExecutor()
		_, _ = manager.CloseAll(context.Background())
		if launcher.launched.Load() {
			select {
			case <-launcher.done:
			case <-time.After(10 * time.Second):
				t.Error("controlled executor did not stop")
			}
		}
	})
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}, managedEnvironmentBindingsHydrated: true}
	schemas := []agentruntime.ToolSchema{{Name: "python", Parameters: agentKernelLanguageToolParameters("Python", "python")}}
	gateway := serverAgentRuntimeToolGateway{server: f.server, taskRun: run, kernel: f.identity, sessionID: f.stream.SessionID, toolSchemas: schemas, toolValidators: agentRuntimeToolValidators(schemas), hasToolSnapshot: true, outputLimitBytes: defaultSessionRunnerOutputLimitBytes}
	x := &resourceContinuationFixture{f: f, backend: backend, launcher: launcher, run: run, model: &resourceContinuationModel{}, schemas: schemas, callIDs: make(chan string, 1)}
	x.engine = agentruntime.Engine{Model: x.model, Tools: gateway}
	return x
}

func (x *resourceContinuationFixture) start(ctx context.Context) <-chan error {
	ctx = withTranscriptRunnerChatRun(ctx, x.run)
	options := SessionRunnerChatOptions{RunnerID: x.f.claim.RunnerID, SessionID: x.f.stream.SessionID, OutputLimitBytes: defaultSessionRunnerOutputLimitBytes}
	x.engine.OnEventError = func(event agentruntime.Event) error {
		if event.Type == agentruntime.EventToolFailed || event.Type == agentruntime.EventToolCompleted {
			x.results.Store(event.ToolCallID, event.Result)
		}
		if event.Type == agentruntime.EventModelResponse && len(event.ToolCalls) > 0 {
			gateway := x.engine.Tools.(serverAgentRuntimeToolGateway)
			if err := x.f.server.checkpointChatModelToolCallsWithContext(ctx, options, x.run, event.ToolCalls, gateway); err != nil {
				return err
			}
			x.callIDs <- event.ToolCalls[0].ID
			return nil
		}
		if event.Type == agentruntime.EventToolFailed {
			x.failed.Add(1)
		}
		return x.f.server.checkpointSessionRunnerToolEvent(ctx, options, x.run, event)
	}
	done := make(chan error, 1)
	go func() {
		_, err := x.engine.Run(ctx, agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Run the controlled calculation once."}}, Tools: x.schemas, MaxToolRounds: 3})
		done <- err
	}()
	return done
}

func TestResourceContinuationKeepsModelAndOperationUntilCapacity(t *testing.T) {
	x := newResourceContinuationFixture(t)
	task, stopTask := context.WithCancel(context.Background())
	defer stopTask()
	ctx := context.WithValue(task, sessionRunLifetimeContextKey{}, task)
	done := x.start(ctx)
	select {
	case <-x.launcher.observed:
	case err := <-done:
		t.Fatalf("engine ended before admission: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("resource admission was not reached")
	}
	callID := <-x.callIDs
	select {
	case err := <-done:
		t.Fatalf("resource wait returned to model: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	before, found, err := x.f.store.GetKernelLocalOperationByToolCall(ctx, x.f.stream.OwnerID, x.f.stream.UID, callID)
	if err != nil || !found || before.State != workspace.KernelLocalOperationStateApproved || before.ExecutionID != "" || x.model.requests.Load() != 1 || x.failed.Load() != 0 || x.launcher.launched.Load() {
		t.Fatalf("waiting operation state=%q execution=%q model=%d failed=%d launched=%t found=%t err=%v", before.State, before.ExecutionID, x.model.requests.Load(), x.failed.Load(), x.launcher.launched.Load(), found, err)
	}
	x.launcher.available.Store(true)
	if err := x.backend.ReconcileStartups(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("admitted original operation did not complete")
	}
	after, found, err := x.f.store.GetKernelLocalOperationByToolCall(ctx, x.f.stream.OwnerID, x.f.stream.UID, callID)
	contents, readErr := os.ReadFile(filepath.Join(x.f.projectPath, "resource-continuation-count.txt"))
	if err != nil || !found || after.OperationID != before.OperationID || after.InputSHA256 != before.InputSHA256 || after.State != workspace.KernelLocalOperationStateCompleted || after.ExecutionID == "" || string(contents) != "x" || readErr != nil || x.model.requests.Load() != 2 || x.failed.Load() != 0 {
		result, _ := x.results.Load(callID)
		t.Fatalf("original continuation state=%q reason=%q operation=%q execution=%q output=%q model=%d failed=%d err=%v read=%v result=%v", after.State, after.ReasonCode, after.OperationID, after.ExecutionID, contents, x.model.requests.Load(), x.failed.Load(), err, readErr, result)
	}
}

func TestResourceContinuationCancellationKeepsDurableApproval(t *testing.T) {
	for _, mode := range []string{"lease_child", "no_lifetime", "task_stop", "drain"} {
		t.Run(mode, func(t *testing.T) {
			x := newResourceContinuationFixture(t)
			task, stopTask := context.WithCancelCause(context.Background())
			defer stopTask(nil)
			request, stopRequest := context.WithCancelCause(task)
			defer stopRequest(nil)
			ctx := context.Context(request)
			if mode != "no_lifetime" {
				ctx = context.WithValue(ctx, sessionRunLifetimeContextKey{}, task)
			}
			done := x.start(ctx)
			select {
			case <-x.launcher.observed:
			case err := <-done:
				t.Fatalf("engine ended before admission: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("resource admission was not reached")
			}
			callID := <-x.callIDs
			want := error(context.Canceled)
			switch mode {
			case "task_stop":
				want = ErrGenerationStopped
				stopTask(want)
			case "drain":
				want = ErrRuntimeDraining
				stopTask(want)
			default:
				stopRequest(want)
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("queue cancellation cause=%v want=%v", err, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled resource waiter did not exit")
			}
			if (mode == "lease_child" || mode == "no_lifetime") && task.Err() != nil {
				t.Fatal("request cancellation stopped the logical task")
			}
			operation, found, err := x.f.store.GetKernelLocalOperationByToolCall(context.Background(), x.f.stream.OwnerID, x.f.stream.UID, callID)
			if err != nil || !found || operation.State != workspace.KernelLocalOperationStateApproved || operation.ExecutionID != "" || operation.PreparedAt != nil || x.model.requests.Load() != 1 || x.failed.Load() != 0 || x.launcher.launched.Load() {
				t.Fatalf("queue interruption terminalized or executed approval: state=%q execution=%q model=%d failures=%d launch=%t found=%t err=%v", operation.State, operation.ExecutionID, x.model.requests.Load(), x.failed.Load(), x.launcher.launched.Load(), found, err)
			}
			if mode != "lease_child" {
				return
			}
			// The old runner ended, but the durable approved source is still the
			// recovery authority. Do not ask the model for a replacement proposal.
			recoveryCtx := context.WithValue(context.Background(), sessionRunLifetimeContextKey{}, task)
			recoveredRun := &sessionRunnerChatRun{SessionID: x.f.stream.SessionID, Attempt: int(x.f.claim.Attempt), ClaimToken: x.f.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: x.f.stream, Claim: x.f.claim}, managedEnvironmentBindingsHydrated: true}
			x.launcher.available.Store(true)
			if err := x.backend.ReconcileStartups(recoveryCtx); err != nil {
				t.Fatal(err)
			}
			options := SessionRunnerChatOptions{RunnerID: x.f.claim.RunnerID, SessionID: x.f.stream.SessionID, OutputLimitBytes: defaultSessionRunnerOutputLimitBytes}
			if resumed, err := x.f.server.resumeApprovedAgentKernelOperations(recoveryCtx, options, recoveredRun); err != nil || !resumed {
				t.Fatalf("same-source recovery resumed=%t err=%v", resumed, err)
			}
			if _, err := x.f.server.resumeApprovedAgentKernelOperations(recoveryCtx, options, recoveredRun); err != nil {
				t.Fatal(err)
			}
			after, found, err := x.f.store.GetKernelLocalOperationByToolCall(recoveryCtx, x.f.stream.OwnerID, x.f.stream.UID, callID)
			contents, readErr := os.ReadFile(filepath.Join(x.f.projectPath, "resource-continuation-count.txt"))
			if err != nil || !found || after.OperationID != operation.OperationID || after.InputSHA256 != operation.InputSHA256 || after.State != workspace.KernelLocalOperationStateCompleted || string(contents) != "x" || readErr != nil || x.model.requests.Load() != 1 {
				t.Fatalf("recovery changed or duplicated the source: state=%q reason=%q op=%q output=%q model=%d err=%v read=%v result=%s", after.State, after.ReasonCode, after.OperationID, contents, x.model.requests.Load(), err, readErr, after.ResultJSON)
			}
		})
	}
}
