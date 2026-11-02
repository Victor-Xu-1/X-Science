package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"x-science/internal/agentruntime"
)

type kernelPreparePhase uint8

const (
	kernelPrepareSource kernelPreparePhase = iota
	kernelPrepareGeneration
	kernelPrepareSpec
	kernelPrepareSession
	kernelPrepareDurable
	kernelPreparePhaseCount
)

type kernelPrepareSpan struct {
	Stage   string `json:"stage"`
	US      *int64 `json:"elapsed_us,omitempty"`
	Unknown bool   `json:"elapsed_unknown,omitempty"`
}

// Missing spans or missing audit records are unknown, never zero-cost work or
// proof of readiness. This advisory shape is not a model-call usage record.
type kernelPrepareDiagnostic struct {
	Version           int                 `json:"version"`
	OperationID       string              `json:"operation_id"`
	Attempt           int                 `json:"attempt"`
	RunnerAttempt     int64               `json:"runner_attempt"`
	Outcome           string              `json:"outcome"`
	US                *int64              `json:"elapsed_us,omitempty"`
	Unknown           bool                `json:"elapsed_unknown,omitempty"`
	KernelGeneration  int64               `json:"kernel_generation,omitempty"`
	BackendGeneration int64               `json:"backend_generation,omitempty"`
	SessionReused     *bool               `json:"session_reused,omitempty"`
	Spans             []kernelPrepareSpan `json:"spans"`
}

type kernelPreparePhaseTiming struct {
	started time.Time
	span    kernelPrepareSpan
	seen    bool
	done    bool
}

// Diagnostic state belongs to one existing invocation. It never owns execution,
// authorization, retries or lifecycle state, and sealing performs no I/O.
type agentKernelPrepareTiming struct {
	mu      sync.Mutex
	now     func() time.Time
	started time.Time
	sealed  bool
	phases  [kernelPreparePhaseCount]kernelPreparePhaseTiming
	record  kernelPrepareDiagnostic
	publish func(kernelPrepareDiagnostic)
}

type kernelPrepareTimingContextKey struct{}

func (s *Server) withAgentKernelPrepareTiming(ctx context.Context, call *agentruntime.ToolCall) (context.Context, *agentKernelPrepareTiming) {
	if ctx == nil {
		return ctx, nil
	}
	run, _ := ctx.Value(transcriptRunnerChatRunContextKey{}).(*sessionRunnerChatRun)
	if s == nil || s.runtimeStore == nil || call == nil || run == nil || run.Transcript == nil ||
		run.Attempt <= 0 || run.Transcript.Claim.Attempt <= 0 {
		return ctx, nil
	}
	operationID := run.KernelOperationIDs[call.ID]
	if operationID == "" || len(operationID) > 128 {
		return ctx, nil
	}
	timing := newAgentKernelPrepareTiming(kernelPrepareDiagnostic{
		Version: 1, OperationID: operationID, Attempt: run.Attempt, RunnerAttempt: run.Transcript.Claim.Attempt,
	}, time.Now, run.kernelPrepareDiagnostics.append)
	return context.WithValue(ctx, kernelPrepareTimingContextKey{}, timing), timing
}

func newAgentKernelPrepareTiming(binding kernelPrepareDiagnostic, now func() time.Time, publish func(kernelPrepareDiagnostic)) *agentKernelPrepareTiming {
	if now == nil {
		now = time.Now
	}
	return &agentKernelPrepareTiming{now: now, started: now(), record: binding, publish: publish}
}

func agentKernelPrepareTimingFromContext(ctx context.Context) *agentKernelPrepareTiming {
	if ctx == nil {
		return nil
	}
	timing, _ := ctx.Value(kernelPrepareTimingContextKey{}).(*agentKernelPrepareTiming)
	return timing
}

func (timing *agentKernelPrepareTiming) begin(stage kernelPreparePhase) func() {
	if timing == nil || stage >= kernelPreparePhaseCount {
		return func() {}
	}
	timing.mu.Lock()
	phase := &timing.phases[stage]
	if timing.sealed || phase.seen {
		timing.mu.Unlock()
		return func() {}
	}
	phase.started, phase.seen = timing.now(), true
	phase.span.Stage = [...]string{"source_preparation", "managed_generation", "session_spec", "ensure_session", "durable_prepare"}[stage]
	timing.mu.Unlock()
	return func() {
		timing.mu.Lock()
		defer timing.mu.Unlock()
		if !timing.sealed && !phase.done {
			phase.span.US, phase.span.Unknown = kernelPrepareElapsed(phase.started, timing.now())
			phase.done = true
		}
	}
}

// No arbitrary cap truncates a long preparation. A backwards or saturated Go
// duration is explicitly unknown; microseconds fit exactly in JSON numbers.
func kernelPrepareElapsed(start, end time.Time) (*int64, bool) {
	duration := end.Sub(start)
	if duration < 0 || duration == time.Duration(1<<63-1) {
		return nil, true
	}
	value := duration.Microseconds()
	return &value, false
}

func (timing *agentKernelPrepareTiming) bindSession(kernel, backend int64, reused *bool) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	defer timing.mu.Unlock()
	if !timing.sealed {
		timing.record.KernelGeneration, timing.record.BackendGeneration = kernel, backend
		if reused != nil {
			value := *reused
			timing.record.SessionReused = &value
		}
	}
}

func (timing *agentKernelPrepareTiming) finishReturn(err error) {
	outcome := "returned_before_prepare"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		outcome = "cancelled_before_prepare"
	} else if err != nil {
		outcome = "error_before_prepare"
	}
	timing.finish(outcome)
}

func (timing *agentKernelPrepareTiming) finish(outcome string) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	if timing.sealed {
		timing.mu.Unlock()
		return
	}
	timing.sealed = true
	ended := timing.now()
	record := timing.record
	switch outcome {
	case "prepared", "returned_before_prepare", "cancelled_before_prepare", "error_before_prepare":
	default:
		outcome = "unknown"
	}
	record.Outcome = outcome
	record.US, record.Unknown = kernelPrepareElapsed(timing.started, ended)
	record.Spans = make([]kernelPrepareSpan, 0, kernelPreparePhaseCount)
	for _, phase := range timing.phases {
		if phase.seen {
			if !phase.done {
				phase.span.US, phase.span.Unknown = kernelPrepareElapsed(phase.started, ended)
			}
			record.Spans = append(record.Spans, phase.span)
		}
	}
	timing.mu.Unlock()
	if timing.publish != nil {
		timing.publish(record)
	}
}
