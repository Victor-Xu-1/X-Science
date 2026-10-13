package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

type earlyProgressCompleteOnlyModel struct {
	response agentruntime.ModelResponse
	err      error
	calls    int
}

func (model *earlyProgressCompleteOnlyModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.calls++
	return model.response, model.err
}

type earlyProgressNoContentWrapper struct{ agentruntime.ModelClient }

func (model earlyProgressNoContentWrapper) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, _ func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	return model.Complete(ctx, request)
}

func TestResponseContractDecodesCompleteOnlyObserverWithoutInventedArrival(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			raw := earlyProgressEnvelope("complete-only", "I will inspect the source records.")
			model := &earlyProgressCompleteOnlyModel{response: earlyProgressToolResponse(raw)}
			if failed {
				model.err = context.DeadlineExceeded
			}
			var record map[string]any
			var delegate agentruntime.ModelClient = model
			if nested {
				delegate = earlyProgressNoContentWrapper{delegate}
			}
			observer := &sessionRunnerCommunicationObserver{delegate: delegate, audit: func(value map[string]any) { record = value }}
			contract := &sessionRunnerResponseContractClient{delegate: observer, progressReady: observer.noteProgressReady}
			boundaries := 0
			response, err := contract.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
					boundaries++
					observer.notePublication()
				}
				return nil
			})
			observer.recordBoundary(true)
			want := 1
			if failed {
				want = 0
			}
			if model.calls != 1 || boundaries != want || failed && !errors.Is(err, context.DeadlineExceeded) || !failed && err != nil {
				t.Fatalf("failed=%t calls=%d boundaries=%d error=%v", failed, model.calls, boundaries, err)
			}
			if record["response_finished_at"] == nil || record["first_native_content_at"] != nil || record["native_stream_bytes"] != 0 {
				t.Fatalf("terminal response invented streaming arrival: %v", record)
			}
			if !failed && (record["first_publication_at"] == nil || response.Message.Content != "I will inspect the source records." || len(response.Message.ToolCalls) != 1) {
				t.Fatalf("successful terminal public block lost: response=%+v audit=%v", response, record)
			}
		}
	}
}

type earlyProgressModel struct {
	chunks     []string
	response   agentruntime.ModelResponse
	err        error
	afterChunk func(int)
	calls      int
}

func (model *earlyProgressModel) Complete(context.Context, agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.calls++
	return model.response, model.err
}

func (model *earlyProgressModel) CompleteStream(_ context.Context, _ agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	model.calls++
	for index, chunk := range model.chunks {
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: chunk}); err != nil {
			return agentruntime.ModelResponse{}, err
		}
		if model.afterChunk != nil {
			model.afterChunk(index)
		}
	}
	return model.response, model.err
}

func earlyProgressEnvelope(id, text string) string {
	encoded, _ := json.Marshal(map[string]any{"version": 1, "id": id, "text": text})
	return agentruntime.PublicProgressEnvelopeBegin + string(encoded) + agentruntime.PublicProgressEnvelopeEnd
}

func earlyProgressToolResponse(raw string) agentruntime.ModelResponse {
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", Content: raw,
		ToolCalls: []agentruntime.ToolCall{{ID: "inspect-one", Name: "inspect", Arguments: json.RawMessage(`{"path":"fixture.csv"}`)}}}}
}

func TestResponseContractPublishesClosedBlockBeforeSlowToolTailWithoutOuterReplay(t *testing.T) {
	const text = "I will inspect the available records."
	raw := earlyProgressEnvelope("early", text)
	// Split before the final delimiter: no partial envelope is publication-ready.
	cut := len(raw) - len(agentruntime.PublicProgressEnvelopeEnd)
	boundaries, ready := 0, 0
	var public strings.Builder
	model := &earlyProgressModel{chunks: []string{raw[:cut], raw[cut:], "private draft"}, response: earlyProgressToolResponse(raw + "private draft")}
	model.afterChunk = func(index int) {
		want := 0
		if index > 0 {
			want = 1
		}
		if boundaries != want || ready != want {
			t.Fatalf("before provider return at chunk %d: boundary=%d ready=%d want=%d", index, boundaries, ready, want)
		}
	}
	contract := &sessionRunnerResponseContractClient{delegate: model, progressReady: func(source string) {
		if source != "structured" {
			t.Fatalf("source=%q", source)
		}
		ready++
	}}
	// The outer engine uses this exact decoding path as well. It must not parse
	// the normalized terminal response a second time or replay the public block.
	response, err := agentruntime.CompletePublicProgressStream(context.Background(), contract, agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
			public.WriteString(event.ContentDelta)
		}
		if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
			boundaries++
		}
		return nil
	}, nil)
	if err != nil || model.calls != 1 || boundaries != 1 || public.String() != text || response.Message.Content != text || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("public=%q boundaries=%d calls=%d response=%+v error=%v", public.String(), boundaries, model.calls, response.Message, err)
	}
}

func TestResponseContractTerminalContentMatchesSanitizedIndependentBlocks(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("Long narration ", 30),
		"I will use mcp__private__tool. I will inspect the records.",
		"<think>private fixture</think>I will inspect the records.",
	} {
		t.Run(text[:10], func(t *testing.T) {
			raw := earlyProgressEnvelope("filtered", text)
			model := &earlyProgressModel{chunks: []string{raw}, response: earlyProgressToolResponse(raw)}
			client := &sessionRunnerResponseContractClient{delegate: model}
			var public strings.Builder
			response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventPublicProgressDelta {
					public.WriteString(event.ContentDelta)
				}
				return nil
			})
			want := sessionRunnerPublicProgressNarration(text)
			if err != nil || public.String() != want || response.Message.Content != want {
				t.Fatalf("filtered stream=%q terminal=%q want=%q err=%v", public.String(), response.Message.Content, want, err)
			}
		})
	}
}

func TestResponseContractRetainsAcceptedProgressWithoutPromotingFailedToolTail(t *testing.T) {
	for _, mode := range []string{"transport", "cancel", "publication"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("fixture transport failure")
			raw := earlyProgressEnvelope("early", "I will inspect the records.")
			model := &earlyProgressModel{chunks: []string{raw}, response: earlyProgressToolResponse(raw), err: failure}
			if mode == "cancel" {
				model.afterChunk = func(int) { cancel() }
				failure = context.Canceled
			}
			boundaries := 0
			client := &sessionRunnerResponseContractClient{delegate: model}
			response, err := client.CompleteStream(ctx, agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
				if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
					if mode == "publication" {
						return failure
					}
					boundaries++
				}
				return nil
			})
			want := 1
			if mode == "publication" {
				want = 0
			}
			if !errors.Is(err, failure) || boundaries != want || len(response.Message.ToolCalls) != 0 || response.Message.Content != "" || model.calls != 1 {
				t.Fatalf("error=%v public=%d response=%+v calls=%d", err, boundaries, response.Message, model.calls)
			}
		})
	}
}
