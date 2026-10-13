package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

type communicationTimingModel struct {
	run func(context.Context, func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error)
}

func (model communicationTimingModel) Complete(ctx context.Context, _ agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return model.run(ctx, nil)
}

func (model communicationTimingModel) CompleteStream(ctx context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return model.run(ctx, emit)
}

func assertCommunicationTime(t *testing.T, record map[string]any, name string, want time.Time) {
	t.Helper()
	if got := record[name]; got != want.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("%s = %v, want %s", name, got, want.UTC().Format(time.RFC3339Nano))
	}
}

func TestCommunicationTimingDistinguishesEarlyNativeContentFromSlowTailAndPublication(t *testing.T) {
	start := time.Date(2026, 10, 14, 8, 0, 0, 123, time.FixedZone("fixture", 8*60*60))
	now := start
	const text = "PUBLIC_SENTINEL"
	var records []map[string]any
	published, observed, calls := 0, 0, 0
	observer := &sessionRunnerCommunicationObserver{
		now: func() time.Time { return now }, publicationBytes: func() int { return published },
		audit: func(record map[string]any) { records = append(records, record) },
	}
	observer.delegate = communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		calls++
		now = start.Add(time.Second)
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: text}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		// The clock advances deterministically while the delegate has not returned.
		// Receiving native text is not permission to publish it.
		now = start.Add(30 * time.Second)
		if observed != 1 || published != 0 || len(records) != 0 {
			t.Fatal("observation invented publication or completed the request early")
		}
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		now = start.Add(40 * time.Second)
		return agentruntime.ModelResponse{Message: agentruntime.Message{
			Content: text, ReasoningContent: "PRIVATE_SENTINEL",
			ToolCalls: []agentruntime.ToolCall{{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{"public_progress":"PUBLIC_SENTINEL"}`)}},
		}}, nil
	}}
	request := agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}
	response, err := observer.CompleteStream(context.Background(), request, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventContentDelta {
			observed++
		}
		return nil
	})
	if err != nil || response.Message.Content != text || calls != 1 {
		t.Fatalf("delegate semantics changed: response=%+v calls=%d err=%v", response, calls, err)
	}
	now = start.Add(41 * time.Second)
	observer.noteProgressReady("structured")
	now = start.Add(42 * time.Second)
	observer.notePresentationReady()
	now = start.Add(43 * time.Second)
	published = len(text) // The caller owns successful durable publication.
	observer.notePublication()
	now = start.Add(44 * time.Second)
	observer.noteProgressReady("native")
	observer.notePresentationReady()
	observer.notePublication()
	observer.recordBoundary(true)
	if len(records) != 1 || records[0]["progress_ready_source"] != "structured" || records[0]["published_bytes"] != len(text) {
		t.Fatalf("wrong source, publication, or call count: %v", records)
	}
	for key, elapsed := range map[string]time.Duration{
		"observer_request_started_at": 0, "first_native_content_at": time.Second,
		"first_tool_boundary_at": 30 * time.Second, "response_finished_at": 40 * time.Second,
		"first_progress_ready_at": 41 * time.Second, "presentation_ready_at": 42 * time.Second,
		"first_publication_at": 43 * time.Second,
	} {
		assertCommunicationTime(t, records[0], key, start.Add(elapsed))
	}
	encoded, _ := json.Marshal(records[0])
	if strings.Contains(string(encoded), text) || strings.Contains(string(encoded), "PRIVATE_SENTINEL") {
		t.Fatal("timing audit retained response data")
	}
}

func TestCommunicationTimingDoesNotInventPublicTimestampsForPrivateOrCancelledCalls(t *testing.T) {
	for _, mode := range []string{"private-only", "cancelled", "emit-error"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callbackErr := errors.New("fixture callback stopped")
			var record map[string]any
			observer := &sessionRunnerCommunicationObserver{now: func() time.Time { return now }, audit: func(value map[string]any) { record = value }}
			observer.delegate = communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
				now = now.Add(time.Second)
				if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true}); err != nil {
					return agentruntime.ModelResponse{}, err
				}
				if mode == "cancelled" {
					cancel()
					return agentruntime.ModelResponse{}, ctx.Err()
				}
				return agentruntime.ModelResponse{}, nil
			}}
			_, err := observer.CompleteStream(ctx, agentruntime.ModelRequest{}, func(agentruntime.ModelStreamEvent) error {
				if mode == "emit-error" {
					return callbackErr
				}
				return nil
			})
			if mode == "cancelled" && !errors.Is(err, context.Canceled) || mode == "emit-error" && !errors.Is(err, callbackErr) || mode == "private-only" && err != nil {
				t.Fatalf("error/cancellation identity changed: %v", err)
			}
			observer.recordBoundary(false)
			observer.noteProgressReady("native") // A settled request is inactive.
			observer.notePresentationReady()
			observer.notePublication()
			for _, key := range []string{"first_native_content_at", "first_tool_boundary_at", "first_progress_ready_at", "progress_ready_source", "presentation_ready_at", "first_publication_at"} {
				if _, exists := record[key]; exists {
					t.Fatalf("%s invented %s", mode, key)
				}
			}
			assertCommunicationTime(t, record, "response_finished_at", now)
		})
	}
}

func TestCommunicationTimingIsolatesSequentialRequestsAndRejectsUnknownSources(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	var records []map[string]any
	observer := &sessionRunnerCommunicationObserver{now: func() time.Time { return now }, audit: func(record map[string]any) { records = append(records, record) }}
	observer.delegate = communicationTimingModel{run: func(_ context.Context, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		return agentruntime.ModelResponse{}, nil
	}}
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.noteProgressReady("native")
	observer.notePresentationReady()
	observer.notePublication()
	now = now.Add(time.Minute)
	// Starting the next request flushes the previous pending audit first.
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.noteProgressReady("PRIVATE_SOURCE_SENTINEL")
	observer.noteProgressReady("localized") // Presentation readiness is a separate observation.
	observer.recordBoundary(false)
	if len(records) != 2 || records[0]["first_publication_at"] == nil {
		t.Fatalf("lost independent request records: %v", records)
	}
	assertCommunicationTime(t, records[1], "observer_request_started_at", now)
	for _, key := range []string{"first_native_content_at", "first_tool_boundary_at", "first_progress_ready_at", "progress_ready_source", "presentation_ready_at", "first_publication_at"} {
		if _, exists := records[1][key]; exists {
			t.Fatalf("earlier request leaked %s into the next request", key)
		}
	}
	encoded, _ := json.Marshal(records)
	if strings.Contains(string(encoded), "PRIVATE_SOURCE_SENTINEL") {
		t.Fatal("unapproved source reached the audit")
	}
}

func TestCommunicationTimingNilHooksAndDefaultClockAreSafe(t *testing.T) {
	var observer *sessionRunnerCommunicationObserver
	observer.noteProgressReady("native")
	observer.notePresentationReady()
	observer.notePublication()
	observer.recordBoundary(false)
	observer = &sessionRunnerCommunicationObserver{}
	observer.noteProgressReady("native")
	observer.notePresentationReady()
	observer.notePublication()
	timing := newRunnerCommunicationTiming(nil)
	timing.noteNativeContent()
	timing.noteResponseFinished()
	record := map[string]any{}
	timing.addTo(record)
	for _, key := range []string{"observer_request_started_at", "first_native_content_at", "response_finished_at"} {
		value, ok := record[key].(string)
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if !ok || err != nil || parsed.IsZero() || !strings.HasSuffix(value, "Z") {
			t.Fatalf("missing UTC default-clock observation: %s=%v", key, record[key])
		}
	}
}

type nonStreamingCommunicationTimingModel struct{ calls int }

func (model *nonStreamingCommunicationTimingModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.calls++
	return agentruntime.ModelResponse{Message: agentruntime.Message{Content: "ready"}}, nil
}

func TestCommunicationTimingNonStreamingDelegateHasNoInventedArrival(t *testing.T) {
	model := &nonStreamingCommunicationTimingModel{}
	var record map[string]any
	observer := &sessionRunnerCommunicationObserver{delegate: model, audit: func(value map[string]any) { record = value }}
	emitted := 0
	response, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(agentruntime.ModelStreamEvent) error { emitted++; return nil })
	observer.recordBoundary(false)
	if err != nil || response.Message.Content != "ready" || model.calls != 1 || emitted != 0 {
		t.Fatalf("nonstreaming invocation changed: calls=%d emitted=%d err=%v", model.calls, emitted, err)
	}
	if record["response_finished_at"] == nil || record["first_native_content_at"] != nil || record["first_publication_at"] != nil {
		t.Fatalf("nonstreaming response invented streaming or publication: %v", record)
	}
	if record["native_stream_bytes"] != 0 {
		t.Fatalf("terminal adaptation counted as native streaming: %v", record)
	}
}

func TestCommunicationTimingKeepsPresentationConversionSeparateFromPendingAgent(t *testing.T) {
	start := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := start
	var records []map[string]any
	published, calls := 0, 0
	observer := &sessionRunnerCommunicationObserver{
		now: func() time.Time { return now }, publicationBytes: func() int { return published },
		audit: func(record map[string]any) { records = append(records, record) },
		delegate: communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
			calls++
			now = now.Add(time.Second)
			if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "Text"}); err != nil {
				return agentruntime.ModelResponse{}, err
			}
			now = now.Add(time.Second)
			return agentruntime.ModelResponse{Message: agentruntime.Message{Content: "Text"}}, nil
		}},
	}
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.noteProgressReady("structured")
	now = start.Add(3 * time.Second)
	if _, err := observer.CompleteStream(withAuxiliaryContextUsage(context.Background()), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(records) != 1 || records[0]["request_scope"] != "presentation" || records[0]["first_publication_at"] != nil {
		t.Fatalf("presentation altered primary scope: calls=%d records=%v", calls, records)
	}
	assertCommunicationTime(t, records[0], "observer_request_started_at", start.Add(3*time.Second))
	now = start.Add(6 * time.Second)
	observer.notePresentationReady()
	published = 4
	observer.notePublication()
	observer.recordBoundary(true)
	if len(records) != 2 || records[1]["request_scope"] != "agent" || records[1]["progress_ready_source"] != "structured" || records[1]["published_bytes"] != 4 {
		t.Fatalf("primary request was replaced by its translation: %v", records)
	}
	assertCommunicationTime(t, records[1], "first_native_content_at", start.Add(time.Second))
	assertCommunicationTime(t, records[1], "response_finished_at", start.Add(2*time.Second))
	assertCommunicationTime(t, records[1], "first_publication_at", start.Add(6*time.Second))
}

func TestCommunicationTimingRecognizesLegacyContentButNotPrivateActivity(t *testing.T) {
	for _, private := range []bool{false, true} {
		var record map[string]any
		observer := &sessionRunnerCommunicationObserver{audit: func(value map[string]any) { record = value },
			delegate: communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
				return agentruntime.ModelResponse{}, emit(agentruntime.ModelStreamEvent{ContentDelta: "candidate", ReasoningActive: private})
			}},
		}
		if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
			t.Fatal(err)
		}
		observer.recordBoundary(false)
		if (record["first_native_content_at"] != nil) == private {
			t.Fatalf("native arrival misclassified private=%t: %v", private, record)
		}
	}
}
