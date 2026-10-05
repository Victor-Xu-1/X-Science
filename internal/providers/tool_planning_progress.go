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

// A tool-enabled request must eventually produce a public answer/progress or
// begin an actual tool call. Private reasoning renews transport liveness, but
// is not an actionable result and cannot indefinitely extend an action-progress
// window. Productive responses keep the existing decoder-progress idle limit;
// this is not a total task or scientific-process deadline.
func toolPlanningStreamContext(
	parent context.Context,
	request agentruntime.ModelRequest,
	timeout time.Duration,
	emit func(agentruntime.ModelStreamEvent) error,
) (context.Context, context.CancelCauseFunc, func(agentruntime.ModelStreamEvent) error, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	if emit == nil {
		emit = func(agentruntime.ModelStreamEvent) error { return nil }
	}
	if len(request.Tools) == 0 {
		return ctx, cancel, emit, func() {}
	}
	var mu sync.Mutex
	var timer *time.Timer
	var generation uint64
	stopped := false
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
		if useful {
			generation++
			if timer != nil {
				timer.Stop()
				timer = nil
			}
		} else if event.ReasoningActive && !stopped && timer == nil {
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
				mu.Unlock()
				cancel(errProviderPlanningNoProgress)
			})
		}
		mu.Unlock()
		return emit(event)
	}
	return ctx, cancel, wrapped, stop
}
