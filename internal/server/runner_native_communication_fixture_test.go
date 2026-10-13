package server

import (
	"context"

	"x-science/internal/agentruntime"
)

type nativeCommunicationFixture struct {
	responses []agentruntime.ModelResponse
	calls     int
}

func (model *nativeCommunicationFixture) Complete(_ context.Context, _ agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	response := model.responses[model.calls]
	model.calls++
	return response, nil
}
func (model *nativeCommunicationFixture) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	response, err := model.Complete(ctx, request)
	if err != nil {
		return response, err
	}
	if emit != nil {
		if response.Message.Content != "" {
			if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: response.Message.Content}); err != nil {
				return agentruntime.ModelResponse{}, err
			}
		}
		if len(response.Message.ToolCalls) > 0 {
			if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary}); err != nil {
				return agentruntime.ModelResponse{}, err
			}
		}
	}
	return response, nil
}
