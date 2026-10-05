package providers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"synon-go/internal/agentruntime"
)

var errProviderPlanningNoProgress = errors.New("provider tool planning exceeded its action-progress window without public output or a tool call")

type providerStreamPlanningDeadline struct {
	Stop            func()
	HeadersReceived func()
}

// Tool-enabled requests already have the first-action deadline below. Keep one
// cancellation authority before headers, not two equal timers racing to choose
// whether the same no-progress interval is replayable. Plain requests retain
// their existing transport header deadline.
func providerStreamHeaderDeadline(request agentruntime.ModelRequest, timeout time.Duration, cancel context.CancelCauseFunc) func() {
	if len(request.Tools) > 0 {
		return func() {}
	}
	timer := time.AfterFunc(timeout, func() { cancel(errOpenAIChatStreamFirstByteTimeout) })
	return func() { timer.Stop() }
}

// A tool-enabled request must produce a public answer/progress or begin an
// actual tool call within its operation window. The initial window starts at
// dispatch, including providers that ignore streaming and trickle unframed
// JSON: incoming bytes alone are not an actionable result. Later private-only
// intervals are bounded too. Productive responses keep the existing decoder
// idle limit; this is not a total task or scientific-process deadline.
func toolPlanningStreamContext(
	parent context.Context,
	request agentruntime.ModelRequest,
	timeout time.Duration,
	emit func(agentruntime.ModelStreamEvent) error,
) (context.Context, context.CancelCauseFunc, func(agentruntime.ModelStreamEvent) error, providerStreamPlanningDeadline) {
	ctx, cancel := context.WithCancelCause(parent)
	if emit == nil {
		emit = func(agentruntime.ModelStreamEvent) error { return nil }
	}
	if len(request.Tools) == 0 {
		return ctx, cancel, emit, providerStreamPlanningDeadline{Stop: func() {}, HeadersReceived: func() {}}
	}
	var mu sync.Mutex
	var timer *time.Timer
	var generation uint64
	stopped := false
	headersReceived := false
	arm := func() {
		generation++
		currentGeneration := generation
		timer = time.AfterFunc(timeout, func() {
			mu.Lock()
			if stopped || generation != currentGeneration {
				mu.Unlock()
				return
			}
			stopped = true
			generation++
			cause := errProviderPlanningNoProgress
			if !headersReceived {
				// Header absence is a transport availability failure, not evidence
				// of private-only generation. Preserve its bounded retry contract.
				cause = errOpenAIChatStreamFirstByteTimeout
			}
			mu.Unlock()
			cancel(cause)
		})
	}
	mu.Lock()
	arm()
	mu.Unlock()
	stop := func() {
		mu.Lock()
		defer mu.Unlock()
		stopped = true
		generation++
		if timer != nil {
			timer.Stop()
		}
	}
	wrapped := func(event agentruntime.ModelStreamEvent) error {
		useful := strings.TrimSpace(event.ContentDelta) != "" || event.Kind == agentruntime.ModelStreamEventToolCallBoundary
		mu.Lock()
		headersReceived = true // A decoded event necessarily follows response headers.
		if useful {
			generation++
			if timer != nil {
				timer.Stop()
				timer = nil
			}
		} else if event.ReasoningActive && !stopped && timer == nil {
			arm()
		}
		mu.Unlock()
		return emit(event)
	}
	return ctx, cancel, wrapped, providerStreamPlanningDeadline{
		Stop: stop,
		HeadersReceived: func() {
			mu.Lock()
			headersReceived = true
			mu.Unlock()
		},
	}
}
