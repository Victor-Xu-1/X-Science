package server

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
	"unicode"

	"x-science/internal/agentruntime"
)

// Optional numeric projection: existing request/provider counters keep their
// original meaning. No streamed text or private reasoning is persisted here.
type runnerContextProgress struct {
	Phase        string    `json:"phase"`
	ObservedAt   time.Time `json:"observedAt"`
	UsedTokens   int       `json:"usedTokens"`
	OutputTokens int       `json:"outputTokens"`
}

const (
	contextProgressInterval     = time.Second
	contextProgressWriteTimeout = 25 * time.Millisecond
)

type contextStreamProgress struct {
	mu                 sync.Mutex
	recorder           *sessionContextUsageRecorder
	snapshot           *runnerContextUsage
	ascii, nonASCII    int
	lastWrite          time.Time
	closed             bool
	writeBackpressured bool
	writeError         bool
	now                func() time.Time
}

func (recorder *sessionContextUsageRecorder) streamProgress(snapshot *runnerContextUsage) *contextStreamProgress {
	if recorder == nil || snapshot == nil {
		return nil
	}
	return &contextStreamProgress{recorder: recorder, snapshot: snapshot, now: time.Now}
}

func (progress *contextStreamProgress) observe(ctx context.Context, event agentruntime.ModelStreamEvent) {
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
	phaseChanged := progress.snapshot.Progress == nil || progress.snapshot.Progress.Phase != phase
	progress.snapshot.Progress = &runnerContextProgress{Phase: phase, ObservedAt: now.UTC(),
		UsedTokens: progress.snapshot.UsedTokens + output, OutputTokens: output}
	// A one-off boundary may be followed only by private argument fragments.
	// Publish its real phase now; throttle repeated samples within that phase.
	if (!phaseChanged || progress.writeBackpressured) && !progress.lastWrite.IsZero() &&
		now.Sub(progress.lastWrite) < contextProgressInterval {
		return
	}
	progress.lastWrite = now
	// Optional sampling must yield to provider delivery and cancellation. After
	// a failed write, even phase changes share the normal retry interval.
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, contextProgressWriteTimeout)
	defer cancel()
	err := progress.recorder.persistProgress(writeCtx, *progress.snapshot)
	progress.writeBackpressured = err != nil
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !progress.writeError {
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

func (client *sessionRunnerDynamicModelClient) recordContextPressure(ctx context.Context, request agentruntime.ModelRequest, failure error, resolved sessionRunnerResolvedModelClient) {
	var pressure *sessionRunnerRequestContextPressureError
	if !errors.As(failure, &pressure) {
		return
	}
	recorder := contextUsageRecorderForCall(ctx, client.contextUsage)
	// Admission runs after model resolution, before provider dispatch.
	ctx, cancel := context.WithTimeout(ctx, contextProgressWriteTimeout)
	defer cancel()
	snapshot := recorder.beginObservation(ctx, "context_preparation", resolved.model, request, contextCapacityForModel(resolved.contextProfile, client.contextOptions))
	if snapshot == nil {
		return
	}
	snapshot.Progress = &runnerContextProgress{Phase: "compacting", ObservedAt: time.Now().UTC(), UsedTokens: snapshot.UsedTokens}
	if err := recorder.persist(ctx, *snapshot, true); err != nil {
		log.Printf("context_usage_pressure_write_failed session=%s: %v", recorder.sessionID, err)
	}
}
