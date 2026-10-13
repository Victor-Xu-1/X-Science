package agentruntime

import (
	"context"
	"errors"
)

// CompletePublicProgressStream decodes one delegate response through the same
// public-block parser as Engine. It does not run an agent loop, execute tools,
// retry, or publish execution events. Wrappers can therefore validate language and
// publish complete public blocks before the rest of a tool response arrives.
func CompletePublicProgressStream(ctx context.Context, delegate ModelClient, request ModelRequest, emit func(ModelStreamEvent) error, presentationDiagnostic func()) (ModelResponse, error) {
	if delegate == nil {
		return ModelResponse{}, errors.New("public progress delegate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	engine := Engine{Model: terminalPublicProgressClient{delegate}, OnModelDelta: emit}
	if presentationDiagnostic != nil {
		engine.OnEventError = func(event Event) error {
			if event.Type == EventPresentationDiagnostic {
				presentationDiagnostic()
			}
			return nil
		}
	}
	response, _, err := engine.completeModelRound(ctx, request, false)
	return response, err
}

// Wrappers can advertise streaming while their selected provider is Complete-
// only. Adapt a successful terminal-only response at this one decoder boundary;
// never substitute terminal bytes after any actual content was streamed.
type terminalPublicProgressClient struct{ delegate ModelClient }

func (client terminalPublicProgressClient) Complete(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	return client.delegate.Complete(ctx, request)
}

func (client terminalPublicProgressClient) CompleteStream(ctx context.Context, request ModelRequest, emit func(ModelStreamEvent) error) (ModelResponse, error) {
	contentObserved := false
	var response ModelResponse
	var err error
	if streaming, ok := client.delegate.(StreamingModelClient); ok {
		response, err = streaming.CompleteStream(ctx, request, func(event ModelStreamEvent) error {
			if event.ContentDelta != "" && !event.ReasoningActive {
				contentObserved = true
			}
			return emit(event)
		})
	} else {
		response, err = client.delegate.Complete(ctx, request)
	}
	if err != nil {
		return response, err
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	if !contentObserved && response.Message.Content != "" {
		if err := emit(ModelStreamEvent{Kind: ModelStreamEventContentDelta, ContentDelta: response.Message.Content}); err != nil {
			return response, err
		}
	}
	return response, nil
}
