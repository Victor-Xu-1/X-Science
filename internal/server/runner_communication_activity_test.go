package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestCommunicationPrivateActivityIsBoundedAndCallScoped(t *testing.T) {
	start := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := start
	var records []map[string]any
	wantError := errors.New("provider stopped")
	calls := 0
	observer := &sessionRunnerCommunicationObserver{
		now: func() time.Time { return now }, audit: func(record map[string]any) { records = append(records, record) },
	}
	observer.delegate = communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		calls++
		if calls == 1 {
			for index, event := range []agentruntime.ModelStreamEvent{
				{Kind: agentruntime.ModelStreamEventPrivateReasoning},
				{ReasoningActive: true},
				{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true},
			} {
				now = start.Add(time.Duration(index+1) * time.Second)
				if err := emit(event); err != nil {
					return agentruntime.ModelResponse{}, err
				}
			}
			return agentruntime.ModelResponse{Message: agentruntime.Message{ReasoningContent: "PRIVATE_BODY_SENTINEL"}}, wantError
		}
		return agentruntime.ModelResponse{Message: agentruntime.Message{ReasoningContent: "PRIVATE_TERMINAL_ONLY_SENTINEL"}}, nil
	}}
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); !errors.Is(err, wantError) {
		t.Fatalf("provider failure identity changed: %v", err)
	}
	if len(records) != 1 || records[0]["private_activity_event_count"] != uint32(3) || records[0]["private_reasoning_observed"] != true {
		t.Fatalf("wrong failed-call activity: %v", records)
	}
	assertCommunicationTime(t, records[0], "first_private_activity_at", start.Add(time.Second))
	assertCommunicationTime(t, records[0], "last_private_activity_at", start.Add(3*time.Second))
	if records[0]["first_native_content_at"] != nil || records[0]["first_publication_at"] != nil || records[0]["published_bytes"] != 0 {
		t.Fatal("private activity became public progress")
	}
	now = start.Add(time.Minute)
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.recordBoundary(false)
	if len(records) != 2 || records[1]["private_activity_event_count"] != uint32(0) || records[1]["private_reasoning_observed"] != false || records[1]["first_private_activity_at"] != nil || records[1]["last_private_activity_at"] != nil {
		t.Fatalf("previous call activity leaked into a zero-activity call: %v", records)
	}
	encoded, err := json.Marshal(records)
	if err != nil || strings.Contains(string(encoded), "PRIVATE_") || strings.Contains(string(encoded), wantError.Error()) {
		t.Fatalf("private content or raw failure escaped into activity metadata: marshal error=%v", err)
	}
}

func TestCommunicationPrivateOnlyCallbacksKeepOneConsistentConcurrentObservation(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	var record map[string]any
	observer := &sessionRunnerCommunicationObserver{
		now: func() time.Time { return now }, audit: func(value map[string]any) { record = value },
		delegate: communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
			var callbacks sync.WaitGroup
			for index := 0; index < 16; index++ {
				callbacks.Add(1)
				go func() {
					defer callbacks.Done()
					if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPrivateReasoning, ReasoningActive: true}); err != nil {
						t.Errorf("private-only observation failed: %v", err)
					}
				}()
			}
			callbacks.Wait()
			return agentruntime.ModelResponse{}, nil
		}},
	}
	if _, err := observer.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	observer.recordBoundary(false)
	if record["private_activity_event_count"] != uint32(16) || record["private_reasoning_observed"] != true {
		t.Fatalf("private-only callbacks lost or double-counted activity: %v", record)
	}
	assertCommunicationTime(t, record, "first_private_activity_at", now)
	assertCommunicationTime(t, record, "last_private_activity_at", now)
}

func TestCommunicationPrivateActivityPreservesObservedWallClockAdjustment(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 10, 0, time.UTC)
	timing := newRunnerCommunicationTiming(func() time.Time { return now })
	timing.notePrivateActivity()
	first := now
	now = now.Add(-time.Second)
	timing.notePrivateActivity()
	record := map[string]any{}
	timing.addTo(record)
	assertCommunicationTime(t, record, "first_private_activity_at", first)
	assertCommunicationTime(t, record, "last_private_activity_at", now)
	if record["private_activity_event_count"] != uint32(2) {
		t.Fatalf("wall-clock change altered callback count: %v", record)
	}
}

func TestCommunicationPrivateActivityCountSaturatesWithoutLosingLastObservation(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	timing := newRunnerCommunicationTiming(func() time.Time { return now })
	timing.privateActivityEvents = maxRunnerPrivateActivityEvents - 1
	timing.notePrivateActivity()
	first := now
	now = now.Add(time.Second)
	timing.notePrivateActivity()
	record := map[string]any{}
	timing.addTo(record)
	if record["private_activity_event_count"] != maxRunnerPrivateActivityEvents {
		t.Fatalf("private activity count wrapped: %v", record)
	}
	assertCommunicationTime(t, record, "first_private_activity_at", first)
	assertCommunicationTime(t, record, "last_private_activity_at", now)
	var absent *runnerCommunicationTiming
	absent.notePrivateActivity()
}
