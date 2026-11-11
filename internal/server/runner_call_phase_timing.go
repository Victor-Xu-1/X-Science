package server

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/persistence/runtimekv"
	"x-science/internal/providers"
)

const maxCallPhaseEntries = 64 // Observation retention only; never an execution budget.

type runnerCallPhaseSpan struct {
	Stage   string `json:"stage"`
	BeginUs int64  `json:"beginUs"`
	EndUs   *int64 `json:"endUs,omitempty"`
}

type runnerCallTransportTiming struct {
	Attempt   int              `json:"attempt"`
	Protocol  string           `json:"protocol"`
	OffsetsUs map[string]int64 `json:"offsetsUs"`
}

// One completed semantic call, not a live execution/lease or billing authority.
type runnerCallPhaseTiming struct {
	Version           int                         `json:"version"`
	Origin            string                      `json:"origin"`
	OffsetsUs         map[string]int64            `json:"offsetsUs"`
	Preparation       []runnerCallPhaseSpan       `json:"preparation,omitempty"`
	TransportAttempts []runnerCallTransportTiming `json:"transportAttempts,omitempty"`
	Truncated         bool                        `json:"truncated"`
	Saturated         bool                        `json:"saturated"`
}

type runnerCallPhaseKey struct{}

// The communication observer owns this scope. A dynamic handoff creates a new
// value per semantic RequestID; callbacks retain their original value forever.
type runnerCallPhaseScope struct {
	current     *runnerCallPhaseRecorder
	origin      time.Time
	now         func() time.Time
	invocations int
}

type runnerCallPhaseRecorder struct {
	mu       sync.Mutex
	origin   time.Time
	now      func() time.Time
	value    runnerCallPhaseTiming
	frozen   bool
	recorder *sessionContextUsageRecorder
	request  *runnerContextUsage
}

func newRunnerCallPhaseScope(now func() time.Time) *runnerCallPhaseScope {
	if now == nil {
		now = time.Now
	}
	scope := &runnerCallPhaseScope{origin: now(), now: now}
	scope.current = scope.newRecorder()
	return scope
}

func (scope *runnerCallPhaseScope) newRecorder() *runnerCallPhaseRecorder {
	return &runnerCallPhaseRecorder{origin: scope.origin, now: scope.now,
		value: runnerCallPhaseTiming{Version: 1, Origin: "observer_entry", OffsetsUs: map[string]int64{}}}
}

func callPhaseScope(ctx context.Context) *runnerCallPhaseScope {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(runnerCallPhaseKey{}).(*runnerCallPhaseScope)
	return value
}

func (scope *runnerCallPhaseScope) nextInvocation() *runnerCallPhaseRecorder {
	if scope == nil {
		return nil
	}
	if scope.invocations > 0 {
		scope.current.freeze()
		scope.current = scope.newRecorder()
	}
	scope.invocations++
	return scope.current
}

func (scope *runnerCallPhaseScope) note(name string) {
	if scope != nil {
		scope.current.note(name)
	}
}

func (scope *runnerCallPhaseScope) freeze() {
	if scope != nil {
		scope.current.freeze()
	}
}

func (phase *runnerCallPhaseRecorder) offset(at time.Time) *int64 {
	if at.IsZero() || at.Before(phase.origin) {
		return nil
	}
	value := at.Sub(phase.origin).Microseconds()
	if value > contextHistorySafeInteger {
		value, phase.value.Saturated = contextHistorySafeInteger, true
	}
	return &value
}

func (phase *runnerCallPhaseRecorder) note(name string) {
	if phase == nil {
		return
	}
	phase.mu.Lock()
	defer phase.mu.Unlock()
	if phase.frozen {
		return
	}
	if _, exists := phase.value.OffsetsUs[name]; !exists {
		if offset := phase.offset(phase.now()); offset != nil {
			phase.value.OffsetsUs[name] = *offset
		}
	}
}

func (phase *runnerCallPhaseRecorder) span(stage string) func() {
	if phase == nil {
		return func() {}
	}
	phase.mu.Lock()
	begin := phase.offset(phase.now())
	if phase.frozen || begin == nil || len(phase.value.Preparation) >= maxCallPhaseEntries {
		if !phase.frozen && begin != nil {
			phase.value.Truncated = true
		}
		phase.mu.Unlock()
		return func() {}
	}
	index := len(phase.value.Preparation)
	phase.value.Preparation = append(phase.value.Preparation, runnerCallPhaseSpan{Stage: stage, BeginUs: *begin})
	phase.mu.Unlock()
	return func() {
		phase.mu.Lock()
		defer phase.mu.Unlock()
		if !phase.frozen && phase.value.Preparation[index].EndUs == nil {
			if end := phase.offset(phase.now()); end != nil && *end >= *begin {
				phase.value.Preparation[index].EndUs = end
			}
		}
	}
}

func (phase *runnerCallPhaseRecorder) transportContext(ctx context.Context) context.Context {
	if phase == nil {
		return ctx
	}
	return providers.WithCompletedTransportObserver(ctx, phase.transport)
}

func (phase *runnerCallPhaseRecorder) transport(attempt providers.CompletedTransportAttempt) {
	phase.mu.Lock()
	defer phase.mu.Unlock()
	if phase.frozen || attempt.Diagnostics == nil || attempt.Attempt <= 0 {
		return
	}
	if len(phase.value.TransportAttempts) >= maxCallPhaseEntries {
		phase.value.Truncated = true
		return
	}
	start := phase.offset(attempt.StartedAt)
	if start == nil {
		return
	}
	diagnostic := attempt.Diagnostics
	offsets := map[string]int64{"attemptEntry": *start}
	for name, value := range map[string]*int64{"doEntered": diagnostic.DoEnteredAfterUs,
		"headersReady": diagnostic.HeadersReadyAfterUs, "firstBodyRead": diagnostic.FirstBodyReadAfterUs,
		"firstDecodedGrowth": diagnostic.FirstDecodedGrowthAfterUs} {
		if value == nil || *value < 0 {
			continue
		}
		if *value > contextHistorySafeInteger-*start {
			offsets[name], phase.value.Saturated = contextHistorySafeInteger, true
		} else {
			offsets[name] = *start + *value
		}
	}
	if diagnostic.CountsSaturated != nil && *diagnostic.CountsSaturated {
		phase.value.Saturated = true
	}
	phase.value.TransportAttempts = append(phase.value.TransportAttempts, runnerCallTransportTiming{
		Attempt: attempt.Attempt, Protocol: attempt.Protocol, OffsetsUs: offsets})
}

func (phase *runnerCallPhaseRecorder) observeEvent(event agentruntime.ModelStreamEvent) {
	if event.ReasoningActive || event.Kind == agentruntime.ModelStreamEventPrivateReasoning {
		phase.note("firstPrivateActivity")
	}
	if !event.ReasoningActive && (event.Kind == agentruntime.ModelStreamEventContentDelta || event.Kind == "") && event.ContentDelta != "" {
		phase.note("firstNativeContent")
	}
	if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
		phase.note("firstToolBoundary")
	}
}

func (phase *runnerCallPhaseRecorder) bind(recorder *sessionContextUsageRecorder, request *runnerContextUsage) {
	if phase == nil {
		return
	}
	phase.mu.Lock()
	defer phase.mu.Unlock()
	if !phase.frozen && request != nil && validContextAssociation(request.Association) {
		phase.recorder, phase.request = recorder, request
	}
}

func (phase *runnerCallPhaseRecorder) freeze() {
	if phase == nil {
		return
	}
	phase.mu.Lock()
	if phase.frozen {
		phase.mu.Unlock()
		return
	}
	phase.frozen = true
	value, recorder, request := phase.value, phase.recorder, phase.request
	phase.mu.Unlock()
	if recorder == nil || request == nil || !validRunnerCallPhaseTiming(&value) {
		return
	}
	// A bounded best-effort metadata write cannot retry the real model request.
	ctx, cancel := context.WithTimeout(context.Background(), contextProgressWriteTimeout)
	defer cancel()
	err := recorder.store.EditNamespaceContext(ctx, contextUsageNamespace(recorder.sessionID), func(entries map[string]runtimekv.Entry) (bool, error) {
		entry, exists := entries["latest"]
		if !exists {
			return false, nil
		}
		if found, err := recorder.contextFrameExists(ctx, true); err != nil || !found {
			return false, err
		}
		previous, err := decodeRunnerContextUsage(entry)
		if err != nil {
			return false, err
		}
		if previous.ObservationKind != "model_call" || (previous.State != "complete" && previous.State != "failed") ||
			previous.RequestID != request.RequestID || previous.Attempt != request.Attempt ||
			!validContextAssociation(previous.Association) || !reflect.DeepEqual(previous.Association, request.Association) {
			return false, nil
		}
		if allowed, err := recorder.allowsContextUsageUpdate(entry, previous, true); err != nil || !allowed {
			return false, err
		}
		previous.PhaseTiming = &value
		if err := recordRunnerContextHistory(entries, previous); err != nil {
			return false, err
		}
		entries["latest"] = runtimekv.Entry{Value: previous}
		return true, nil
	})
	if err != nil {
		log.Printf("context_call_phase_timing_unavailable")
	}
}

func validRunnerCallPhaseTiming(value *runnerCallPhaseTiming) bool {
	if value == nil {
		return true
	}
	if value.Version != 1 || value.Origin != "observer_entry" || len(value.OffsetsUs) > 6 ||
		len(value.Preparation) > maxCallPhaseEntries || len(value.TransportAttempts) > maxCallPhaseEntries {
		return false
	}
	allowed := map[string]bool{"firstPrivateActivity": true, "firstNativeContent": true, "firstToolBoundary": true,
		"firstPublicProgressReady": true, "firstDurablePublication": true, "delegateFinished": true}
	for key, offset := range value.OffsetsUs {
		if !allowed[key] || offset < 0 || offset > contextHistorySafeInteger {
			return false
		}
	}
	for _, span := range value.Preparation {
		if (span.Stage != "resolve" && span.Stage != "directory_prepare" && span.Stage != "context_budget") || span.BeginUs < 0 || span.BeginUs > contextHistorySafeInteger ||
			(span.EndUs != nil && (*span.EndUs < span.BeginUs || *span.EndUs > contextHistorySafeInteger)) {
			return false
		}
	}
	for _, attempt := range value.TransportAttempts {
		if attempt.Attempt <= 0 || int64(attempt.Attempt) > contextHistorySafeInteger || len(attempt.OffsetsUs) > 5 {
			return false
		}
		switch attempt.Protocol {
		case providers.ProtocolOpenAICompatible, providers.ProtocolOpenAIResponses, providers.ProtocolAzureOpenAI, providers.ProtocolAzureOpenAIResponses, providers.ProtocolAnthropic, providers.ProtocolGemini:
		default:
			return false
		}
		start, exists := attempt.OffsetsUs["attemptEntry"]
		if !exists || start < 0 {
			return false
		}
		prior := start
		for _, key := range []string{"attemptEntry", "doEntered", "headersReady", "firstBodyRead", "firstDecodedGrowth"} {
			if offset, present := attempt.OffsetsUs[key]; present {
				if offset < prior || offset > contextHistorySafeInteger {
					return false
				}
				prior = offset
			}
		}
		for key := range attempt.OffsetsUs {
			if key != "attemptEntry" && key != "doEntered" && key != "headersReady" && key != "firstBodyRead" && key != "firstDecodedGrowth" {
				return false
			}
		}
	}
	return true
}

// JSON null/missing values must not acquire Go zero values and become recorded
// facts. Decode this optional object independently of the original call/usage.
func decodeRunnerCallPhaseTiming(raw json.RawMessage) *runnerCallPhaseTiming {
	fields, ok := callPhaseWireObject(raw, "version", "origin", "offsetsUs", "truncated", "saturated")
	if !ok {
		return nil
	}
	if _, ok := callPhaseWireObject(fields["offsetsUs"]); !ok {
		return nil
	}
	for key, required := range map[string][]string{
		"preparation":       {"stage", "beginUs"},
		"transportAttempts": {"attempt", "protocol", "offsetsUs"},
	} {
		array, present := fields[key]
		if !present {
			continue
		}
		var entries []json.RawMessage
		if json.Unmarshal(array, &entries) != nil || len(entries) > maxCallPhaseEntries {
			return nil
		}
		for _, entry := range entries {
			item, ok := callPhaseWireObject(entry, required...)
			if !ok {
				return nil
			}
			if key == "transportAttempts" {
				if _, ok := callPhaseWireObject(item["offsetsUs"], "attemptEntry"); !ok {
					return nil
				}
			}
		}
	}
	var value runnerCallPhaseTiming
	if json.Unmarshal(raw, &value) != nil || !validRunnerCallPhaseTiming(&value) {
		return nil
	}
	return &value
}

func callPhaseWireObject(raw json.RawMessage, required ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, false
	}
	for _, key := range required {
		if _, present := fields[key]; !present {
			return nil, false
		}
	}
	for _, value := range fields {
		if strings.TrimSpace(string(value)) == "null" {
			return nil, false
		}
	}
	return fields, true
}
