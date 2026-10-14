package providers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"x-science/internal/agentruntime"
)

var errProviderPlanningNoProgress = errors.New("provider tool planning exceeded its action-progress window without public output or a tool call")

// This request-scoped observation carries native action evidence, not executable
// arguments or public content. Keep a boundary racing cancellation visible until
// the decoder returns; taking a snapshot when the timer fires can lose it.
type providerPlanningNoProgressError struct {
	toolBoundaryObserved *atomic.Bool
}

func (err *providerPlanningNoProgressError) Error() string {
	return errProviderPlanningNoProgress.Error()
}
func (err *providerPlanningNoProgressError) Unwrap() error { return errProviderPlanningNoProgress }

type providerStreamPlanningDeadline struct {
	Stop              func()
	HeadersReceived   func()
	ObserveGrowth     func(int64)
	NoCompletedAction func(error) error
}

// Tool-enabled requests already have the initial decoded-activity deadline below. Keep one
// cancellation authority before headers, not two equal timers racing to choose
// whether the same no-progress interval is replayable. Plain requests retain
// their existing transport header deadline.
func providerStreamHeaderDeadline(request agentruntime.ModelRequest, timeout time.Duration, cancel context.CancelCauseFunc) func() {
	if len(request.Tools) > 0 {
		return func() {}
	}
	var mu sync.Mutex
	var stopped bool
	var generation uint64
	mu.Lock()
	generation++
	currentGeneration := generation
	timer := time.AfterFunc(timeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped || generation != currentGeneration {
			return
		}
		stopped = true
		// The native context cancellation completes before Stop can return.
		// Otherwise Stop may win after the check but still be cancelled later.
		cancel(errOpenAIChatStreamFirstByteTimeout)
	})
	mu.Unlock()
	return func() {
		mu.Lock()
		stopped = true
		generation++
		mu.Unlock()
		_ = timer.Stop()
	}
}

// Bound the first decoded generation activity, not the first public word.
// Once the decoder admits positive growth, its finite byte budget and semantic
// idle reader own liveness. Public/terminal metadata never substitutes for
// decoder growth; unframed bytes cannot extend the initial window.
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
		return ctx, cancel, emit, providerStreamPlanningDeadline{
			Stop: func() {}, HeadersReceived: func() {}, ObserveGrowth: func(int64) {},
			NoCompletedAction: func(err error) error { return err },
		}
	}
	var mu sync.Mutex
	var timer *time.Timer
	var generation uint64
	stopped := false
	headersReceived := false
	var toolBoundaryObserved atomic.Bool
	var decodedGrowth atomic.Bool
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
			var cause error = &providerPlanningNoProgressError{toolBoundaryObserved: &toolBoundaryObserved}
			if !headersReceived {
				// Header absence is a transport availability failure, not evidence
				// of private-only generation. Preserve its bounded retry contract.
				cause = errOpenAIChatStreamFirstByteTimeout
			}
			cancel(cause)
			mu.Unlock()
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
		mu.Lock()
		headersReceived = true // A decoded event necessarily follows response headers.
		if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
			toolBoundaryObserved.Store(true)
		}
		mu.Unlock()
		return emit(event)
	}
	return ctx, cancel, wrapped, providerStreamPlanningDeadline{
		Stop: stop,
		ObserveGrowth: func(growth int64) {
			if growth <= 0 {
				return
			}
			mu.Lock()
			decodedGrowth.Store(true)
			generation++
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			mu.Unlock()
		},
		NoCompletedAction: func(err error) error {
			if !decodedGrowth.Load() || (!errors.Is(err, errOpenAIChatStreamIdleTimeout) && !errors.Is(err, errProviderResponseTooLarge)) {
				return err
			}
			return newProviderGenerationNoAction(err, &toolBoundaryObserved)
		},
		HeadersReceived: func() {
			mu.Lock()
			headersReceived = true
			mu.Unlock()
		},
	}
}
