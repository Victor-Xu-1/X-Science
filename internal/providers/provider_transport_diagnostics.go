package providers

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const maxTransportDiagnosticInteger int64 = 1<<53 - 1

type ProviderTransportFormat string
type ProviderTransportStopCause string

// Transport stages describe this HTTP attempt, not wire TTFB or public output.
// A missing pointer is unobserved. CountsSaturated covers all numeric counts,
// including microsecond offsets; capped values are lower bounds, not exact.
type ProviderTransportDiagnostics struct {
	Version                   int                         `json:"version"`
	DoEnteredAfterUs          *int64                      `json:"doEnteredAfterUs,omitempty"`
	HeadersReadyAfterUs       *int64                      `json:"headersReadyAfterUs,omitempty"`
	FirstBodyReadAfterUs      *int64                      `json:"firstBodyReadAfterUs,omitempty"`
	FirstDecodedGrowthAfterUs *int64                      `json:"firstDecodedGrowthAfterUs,omitempty"`
	ResponseBodyBytes         *int64                      `json:"responseBodyBytes,omitempty"`
	DecodedGrowthEvents       *int64                      `json:"decodedGrowthEvents,omitempty"`
	CountsSaturated           *bool                       `json:"countsSaturated,omitempty"`
	Format                    *ProviderTransportFormat    `json:"format,omitempty"`
	StopCause                 *ProviderTransportStopCause `json:"stopCause,omitempty"`
}

type providerTransportObservation struct {
	mu                                    sync.Mutex
	started                               time.Time
	parent, request                       context.Context
	doEntered, headers, firstBody, growth *int64
	bodyObserved, decoding                bool
	bodyBytes, growthEvents               int64
	saturated                             bool
	format                                ProviderTransportFormat
}

// CompletedTransportAttempt is passive, per-HTTP-attempt evidence. StartedAt
// retains its monotonic clock for a caller-owned offset projection. It contains
// no prompt, credential, request header or execution identity.
type CompletedTransportAttempt struct {
	Attempt     int
	Protocol    string
	StartedAt   time.Time
	Diagnostics *ProviderTransportDiagnostics
}

type completedTransportObserverKey struct{}

// WithCompletedTransportObserver never changes the outbound request. The
// callback belongs to this context, not the reused client, and runs once at the
// end of each actual attempt. It must not make execution/publication decisions.
func WithCompletedTransportObserver(ctx context.Context, observe func(CompletedTransportAttempt)) context.Context {
	return context.WithValue(ctx, completedTransportObserverKey{}, observe)
}

func (o *providerTransportObservation) publishCompleted(ctx context.Context, attempt int, protocol string) {
	if observe, ok := ctx.Value(completedTransportObserverKey{}).(func(CompletedTransportAttempt)); ok && observe != nil {
		observe(CompletedTransportAttempt{Attempt: attempt, Protocol: protocol, StartedAt: o.started, Diagnostics: o.snapshot(nil, nil)})
	}
}

func (o *providerTransportObservation) enterDo() {
	o.mu.Lock()
	o.doEntered = o.offset()
	o.mu.Unlock()
}

func (o *providerTransportObservation) offset() *int64 {
	if o.started.IsZero() {
		return nil
	}
	elapsed := time.Since(o.started).Microseconds()
	if elapsed < 0 {
		return nil
	}
	if elapsed > maxTransportDiagnosticInteger {
		elapsed, o.saturated = maxTransportDiagnosticInteger, true
	}
	return diagnosticPointer(elapsed)
}

func (o *providerTransportObservation) observeHeaders(body io.ReadCloser) io.ReadCloser {
	o.mu.Lock()
	o.headers, o.bodyObserved = o.offset(), true
	o.mu.Unlock()
	return &providerTransportBody{ReadCloser: body, observation: o}
}

type providerTransportBody struct {
	io.ReadCloser
	observation *providerTransportObservation
}

func (body *providerTransportBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	if n > 0 {
		o := body.observation
		o.mu.Lock()
		if o.firstBody == nil {
			o.firstBody = o.offset()
		}
		o.bodyBytes = o.add(o.bodyBytes, int64(n))
		o.mu.Unlock()
	}
	return n, err
}

func (o *providerTransportObservation) add(current, delta int64) int64 {
	if delta > maxTransportDiagnosticInteger-current {
		o.saturated = true
		return maxTransportDiagnosticInteger
	}
	return current + delta
}

func (o *providerTransportObservation) beginDecode(format ProviderTransportFormat) {
	o.observeFormat(format, true)
}

func (o *providerTransportObservation) observeFormat(format ProviderTransportFormat, decoding bool) {
	o.mu.Lock()
	o.decoding, o.format = o.decoding || decoding, format
	o.mu.Unlock()
}

func (o *providerTransportObservation) observeGrowth(growth int64, jsonOnly bool) {
	if o == nil || growth <= 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.decoding || (jsonOnly && o.format != "json_fallback") {
		return
	}
	if o.growth == nil {
		o.growth = o.offset()
	}
	o.growthEvents = o.add(o.growthEvents, 1)
}

func (o *providerTransportObservation) snapshot(err error, terminal *bool) *ProviderTransportDiagnostics {
	if o == nil {
		return nil
	}
	copyOffset := func(value *int64) *int64 {
		if value == nil {
			return nil
		}
		return diagnosticPointer(*value)
	}
	o.mu.Lock()
	value := ProviderTransportDiagnostics{Version: 1, DoEnteredAfterUs: copyOffset(o.doEntered), HeadersReadyAfterUs: copyOffset(o.headers),
		FirstBodyReadAfterUs: copyOffset(o.firstBody), FirstDecodedGrowthAfterUs: copyOffset(o.growth)}
	if o.bodyObserved {
		value.ResponseBodyBytes = diagnosticPointer(o.bodyBytes)
	}
	if o.decoding {
		value.DecodedGrowthEvents = diagnosticPointer(o.growthEvents)
	}
	if o.format != "" {
		value.Format = diagnosticPointer(o.format)
	}
	if o.bodyObserved || o.decoding {
		value.CountsSaturated = diagnosticPointer(o.saturated)
	}
	o.mu.Unlock()
	value.StopCause = providerTransportCause(o.parent, o.request, err, terminal)
	return &value
}

func providerTransportCause(parent, request context.Context, err error, terminal *bool) *ProviderTransportStopCause {
	if err == nil {
		if terminal != nil && *terminal {
			return diagnosticPointer(ProviderTransportStopCause("terminal_completed"))
		}
		return nil
	}
	// Only a matching original error can establish parent cancellation. A later
	// cancel must not relabel an already completed/independent typed failure.
	if cause := parent.Err(); cause != nil && errors.Is(err, cause) {
		if errors.Is(cause, context.DeadlineExceeded) {
			return diagnosticPointer(ProviderTransportStopCause("parent_deadline"))
		}
		return diagnosticPointer(ProviderTransportStopCause("parent_canceled"))
	}
	causes := []error{err}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		causes = append(causes, context.Cause(request))
	}
	for _, cause := range causes {
		var value ProviderTransportStopCause
		switch {
		case errors.Is(cause, errOpenAIChatStreamFirstByteTimeout):
			value = "header_deadline"
		case errors.Is(cause, errProviderPlanningNoProgress):
			value = "initial_activity_deadline"
		case errors.Is(cause, errOpenAIChatStreamIdleTimeout):
			value = "semantic_idle_deadline"
		case errors.Is(cause, errProviderResponseTooLarge):
			value = "response_limit"
		case errors.Is(cause, errProviderStreamIncomplete):
			value = "incomplete_stream"
		case IsProviderOutputTokenLimit(cause):
			value = "output_limit"
		case errors.Is(cause, errOpenAIChatStreamRead):
			value = "stream_read_error"
		}
		if value != "" {
			return diagnosticPointer(value)
		}
	}
	return nil
}
