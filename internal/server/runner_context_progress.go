package server

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
	"unicode"

	"synon-go/internal/agentruntime"
)

// Optional numeric projection: existing request/provider counters keep their
// original meaning. No streamed text or private reasoning is persisted here.
type runnerContextProgress struct {
	Phase        string    `json:"phase"`
	ObservedAt   time.Time `json:"observedAt"`
	UsedTokens   int       `json:"usedTokens"`
	OutputTokens int       `json:"outputTokens"`
}

const contextProgressInterval = time.Second

type contextStreamProgress struct {
	mu                 sync.Mutex
	recorder           *sessionContextUsageRecorder
	snapshot           *runnerContextUsage
	ascii, nonASCII    int
	lastWrite          time.Time
	closed, writeError bool
	now                func() time.Time
}

func (recorder *sessionContextUsageRecorder) streamProgress(snapshot *runnerContextUsage) *contextStreamProgress {
	if recorder == nil || snapshot == nil {
		return nil
	}
	return &contextStreamProgress{recorder: recorder, snapshot: snapshot, now: time.Now}
}

func (progress *contextStreamProgress) observe(event agentruntime.ModelStreamEvent) {
	if progress == nil {
		return
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.closed {
		return
	}
	if event.Kind == agentruntime.ModelStreamEventPrivateReasoning {
		// Private events convey activity, never a public token estimate.
		event.ContentDelta = ""
	}
	if event.ContentDelta == "" && !event.ReasoningActive && event.Kind != agentruntime.ModelStreamEventToolCallBoundary {
		return
	}
	for _, current := range event.ContentDelta {
		if current <= unicode.MaxASCII {
			progress.ascii++
		} else {
			progress.nonASCII++
		}
	}
	output := progress.nonASCII + (progress.ascii+3)/4
	if output > int(^uint(0)>>1)-progress.snapshot.UsedTokens {
		return
	}
	phase := "generating"
	if event.ReasoningActive && event.ContentDelta == "" {
		phase = "thinking"
	}
	now := progress.now()
	progress.snapshot.Progress = &runnerContextProgress{Phase: phase, ObservedAt: now.UTC(),
		UsedTokens: progress.snapshot.UsedTokens + output, OutputTokens: output}
	if !progress.lastWrite.IsZero() && now.Sub(progress.lastWrite) < contextProgressInterval {
		return
	}
	progress.lastWrite = now
	if err := progress.recorder.persist(*progress.snapshot, true); err != nil && !progress.writeError {
		progress.writeError = true
		log.Printf("context_usage_progress_write_failed session=%s request=%s: %v", progress.snapshot.SessionID, progress.snapshot.RequestID, err)
	}
}

func (progress *contextStreamProgress) finish(response agentruntime.ModelResponse, err error) {
	if progress == nil {
		return
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.closed = true
	progress.recorder.finish(progress.snapshot, response, err)
}

func validRunnerContextProgress(snapshot runnerContextUsage) bool {
	p := snapshot.Progress
	if p == nil {
		return true
	}
	return snapshot.Source == "estimated" && snapshot.State != "complete" && !p.ObservedAt.IsZero() &&
		(p.Phase == "generating" || p.Phase == "thinking" || p.Phase == "compacting") &&
		p.OutputTokens >= 0 && p.UsedTokens >= snapshot.UsedTokens &&
		p.UsedTokens-snapshot.UsedTokens == p.OutputTokens && (p.Phase != "compacting" || p.OutputTokens == 0)
}

func (client *sessionRunnerDynamicModelClient) recordContextPressure(ctx context.Context, request agentruntime.ModelRequest, failure error) {
	var pressure *sessionRunnerRequestContextPressureError
	if !errors.As(failure, &pressure) {
		return
	}
	recorder := contextUsageRecorderForCall(ctx, client.contextUsage)
	// The provider has not been resolved/dispatched. Do not guess a model name.
	snapshot := recorder.begin("", request)
	if snapshot == nil {
		return
	}
	snapshot.Progress = &runnerContextProgress{Phase: "compacting", ObservedAt: time.Now().UTC(), UsedTokens: snapshot.UsedTokens}
	if err := recorder.persist(*snapshot, true); err != nil {
		log.Printf("context_usage_pressure_write_failed session=%s: %v", recorder.sessionID, err)
	}
}
