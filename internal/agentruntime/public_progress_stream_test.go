package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type publicProgressFacadeStatic struct {
	response ModelResponse
	calls    int
}

func (model *publicProgressFacadeStatic) Complete(context.Context, ModelRequest) (ModelResponse, error) {
	model.calls++
	return model.response, nil
}

type publicProgressFacadeStreaming struct {
	publicProgressFacadeStatic
	events []ModelStreamEvent
	err    error
}

func (model *publicProgressFacadeStreaming) CompleteStream(_ context.Context, _ ModelRequest, emit func(ModelStreamEvent) error) (ModelResponse, error) {
	model.calls++
	for _, event := range model.events {
		if err := emit(event); err != nil {
			return ModelResponse{}, err
		}
	}
	return model.response, model.err
}

func TestCompletePublicProgressStreamUsesOneDelegateCall(t *testing.T) {
	raw := publicProgressTestEnvelope("step", "Checking the supplied inputs.") + "candidate"
	for _, streaming := range []bool{false, true} {
		base := publicProgressFacadeStatic{response: ModelResponse{Message: Message{Content: raw}}}
		var delegate ModelClient = &base
		stream := &publicProgressFacadeStreaming{publicProgressFacadeStatic: base, events: []ModelStreamEvent{{Kind: ModelStreamEventContentDelta, ContentDelta: raw}}}
		if streaming {
			delegate = stream
		}
		public := 0
		response, err := CompletePublicProgressStream(nil, delegate, ModelRequest{}, func(event ModelStreamEvent) error {
			if event.Kind == ModelStreamEventPublicProgressBoundary {
				public++
			}
			return nil
		}, nil)
		calls := base.calls
		if streaming {
			calls = stream.calls
		}
		if err != nil || calls != 1 || public != 1 || response.Message.Content != "candidate" {
			t.Fatalf("streaming=%t calls=%d public=%d response=%+v err=%v", streaming, calls, public, response, err)
		}
	}
}

func TestCompletePublicProgressStreamPreservesPrivateAndCancellationBoundaries(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		model := &publicProgressFacadeStreaming{events: []ModelStreamEvent{{Kind: ModelStreamEventPrivateReasoning, ReasoningActive: true}}}
		public := 0
		_, err := CompletePublicProgressStream(ctx, model, ModelRequest{}, func(event ModelStreamEvent) error {
			if event.ContentDelta != "" {
				public++
			}
			return nil
		}, nil)
		cancel()
		if public != 0 || model.calls != 1 || cancelled && !errors.Is(err, context.Canceled) || !cancelled && err != nil {
			t.Fatalf("cancelled=%t calls=%d public=%d err=%v", cancelled, model.calls, public, err)
		}
	}
}

func TestCompletePublicProgressStreamKeepsMalformedProgressDiagnosticWithoutExecutingTools(t *testing.T) {
	raw := PublicProgressEnvelopeBegin + `{broken}` + PublicProgressEnvelopeEnd
	model := &publicProgressFacadeStreaming{
		publicProgressFacadeStatic: publicProgressFacadeStatic{response: ModelResponse{Message: Message{
			Content: raw, ToolCalls: []ToolCall{{ID: "action", Name: "inspect", Arguments: json.RawMessage(`{}`)}},
		}}},
		events: []ModelStreamEvent{{Kind: ModelStreamEventContentDelta, ContentDelta: raw}},
	}
	public, diagnostics := 0, 0
	response, err := CompletePublicProgressStream(context.Background(), model, ModelRequest{}, func(ModelStreamEvent) error {
		public++
		return nil
	}, func() { diagnostics++ })
	if err != nil || model.calls != 1 || public != 0 || diagnostics != 1 || response.Message.Content != "" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("calls=%d public=%d diagnostics=%d response=%+v err=%v", model.calls, public, diagnostics, response, err)
	}
}

func TestCompletePublicProgressStreamDoesNotSwallowPublicationFailure(t *testing.T) {
	raw := publicProgressTestEnvelope("step", "Checking the supplied inputs.")
	model := &publicProgressFacadeStreaming{events: []ModelStreamEvent{{Kind: ModelStreamEventContentDelta, ContentDelta: raw}}}
	want := errors.New("durable publication failed")
	_, err := CompletePublicProgressStream(context.Background(), model, ModelRequest{}, func(ModelStreamEvent) error { return want }, nil)
	if !errors.Is(err, want) || model.calls != 1 {
		t.Fatalf("publication failure hidden or retried: %v calls=%d", err, model.calls)
	}
}

func TestCompletePublicProgressStreamDoesNotReplaceObservedContentWithTerminalEnvelope(t *testing.T) {
	raw := publicProgressTestEnvelope("terminal", "Checking the supplied inputs.")
	for _, content := range []string{"private candidate", publicProgressTestEnvelope("stream", "Checking a different source.")} {
		model := &publicProgressFacadeStreaming{
			publicProgressFacadeStatic: publicProgressFacadeStatic{response: ModelResponse{Message: Message{Content: raw}}},
			events:                     []ModelStreamEvent{{Kind: ModelStreamEventContentDelta, ContentDelta: content}},
		}
		_, err := CompletePublicProgressStream(context.Background(), model, ModelRequest{}, nil, nil)
		var failure *PublicProgressPresentationError
		if !errors.As(err, &failure) || model.calls != 1 {
			t.Fatalf("stream/terminal mismatch was replaced by fallback: error=%v calls=%d", err, model.calls)
		}
	}
}
