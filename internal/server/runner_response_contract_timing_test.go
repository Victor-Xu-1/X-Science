package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestResponseContractTimingIdentifiesValidatedProgressAfterSlowNativeTail(t *testing.T) {
	start := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := start
	var record map[string]any
	published := 0
	observer := &sessionRunnerCommunicationObserver{
		now: func() time.Time { return now }, publicationBytes: func() int { return published },
		audit: func(value map[string]any) { record = value },
	}
	observer.delegate = communicationTimingModel{run: func(_ context.Context, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
		now = start.Add(time.Second)
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: "I will inspect the source."}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		now = start.Add(10 * time.Second)
		if published != 0 || record != nil {
			t.Fatal("a private native candidate became a completed public record")
		}
		return agentruntime.ModelResponse{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{
			ID: "inspect", Name: "inspect", Arguments: json.RawMessage(`{"public_progress":"I will inspect the source."}`),
		}}}}, nil
	}}
	client := &sessionRunnerResponseContractClient{delegate: observer, progressReady: observer.noteProgressReady}
	_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
			now = start.Add(20 * time.Second)
			published++
			observer.notePublication() // Represents this test consumer's accepted boundary, not a database write.
		}
		return nil
	})
	observer.recordBoundary(true)
	if err != nil || published != 1 || record["progress_ready_source"] != "structured" {
		t.Fatalf("wrong validated boundary: published=%d record=%v err=%v", published, record, err)
	}
	assertCommunicationTime(t, record, "first_native_content_at", start.Add(time.Second))
	assertCommunicationTime(t, record, "response_finished_at", start.Add(10*time.Second))
	assertCommunicationTime(t, record, "first_progress_ready_at", start.Add(10*time.Second))
	assertCommunicationTime(t, record, "first_publication_at", start.Add(20*time.Second))
}

func TestResponseContractTimingDoesNotLabelUnvalidatedProgressReady(t *testing.T) {
	for _, tc := range []struct {
		name     string
		model    agentruntime.ModelClient
		expected string
	}{
		{"structured", &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{ToolCalls: []agentruntime.ToolCall{{ID: "a", Name: "inspect", Arguments: json.RawMessage(`{"public_progress":"Check the source."}`)}}}}}}, "structured"},
		{"native", &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Content: "Check the source.", ToolCalls: []agentruntime.ToolCall{{ID: "a", Name: "inspect", Arguments: json.RawMessage(`{}`)}}}}}}, "native"},
		{"malformed", &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Content: "Check the source.", ToolCalls: []agentruntime.ToolCall{{ID: "a", Name: "inspect", Arguments: json.RawMessage(`{`)}}}}}}, ""},
		{"interrupted", interruptedProgressModel{}, ""},
		{"final-only", &nativeCommunicationFixture{responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Content: "A final answer."}}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sources []string
			client := &sessionRunnerResponseContractClient{delegate: tc.model, progressReady: func(source string) { sources = append(sources, source) }}
			_, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, nil)
			if tc.name == "interrupted" && err != context.DeadlineExceeded || tc.name != "interrupted" && err != nil {
				t.Fatalf("timing changed completion semantics: %v", err)
			}
			if tc.expected == "" && len(sources) != 0 || tc.expected != "" && (len(sources) != 1 || sources[0] != tc.expected) {
				t.Fatalf("unexpected progress readiness: %v", sources)
			}
		})
	}
}
