package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"x-science/internal/agentruntime"
)

const runnerPublicProgressField = "public_progress"

func runnerPublicProgressCadence(due bool) string {
	if due {
		return "A public progress update is due. Begin with one complete public progress envelope describing the immediate next action and its purpose, before generating tool arguments. Do not wait to plan every later step."
	}
	return "An additional public progress update is optional. Use a public progress envelope only for materially new progress; do not repeat an unchanged update for parallel calls."
}

// Cadence is request guidance, not a second tool schema or execution gate.
// The public envelope contract has one shared instruction authority. Keep the
// former tool field reserved only so legacy replay stripping cannot remove a
// real executable argument with the same name.
func projectRunnerResponseContract(request agentruntime.ModelRequest, due bool) (agentruntime.ModelRequest, error) {
	for _, tool := range request.Tools {
		properties, _ := tool.Parameters["properties"].(map[string]any)
		if _, collision := properties[runnerPublicProgressField]; collision {
			return request, fmt.Errorf("tool %q reserves the primary response progress field", tool.Name)
		}
	}
	request.Messages = append(append([]agentruntime.Message(nil), request.Messages...), agentruntime.Message{
		Role: "system", Content: runnerPublicProgressCadence(due), ContextUsageSource: agentruntime.ContextUsageSystemPrompt,
	})
	return request, nil
}

type sessionRunnerResponseContractClient struct {
	delegate    agentruntime.ModelClient
	progressDue func() bool
	// Observation only; the canonical publication boundary remains downstream.
	progressReady func(string)
	// Optional invalid-envelope diagnostic, without content or execution events.
	presentationDiagnostic func()
}

func (client *sessionRunnerResponseContractClient) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return client.CompleteStream(ctx, request, nil)
}

// Independent, fully decoded public blocks can precede a complete action.
// Native drafts and legacy tool-field narration still require the complete
// response; neither a partial tool boundary nor a partial JSON field releases it.
func (client *sessionRunnerResponseContractClient) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	projected, err := projectRunnerResponseContract(request, client.progressDue != nil && client.progressDue())
	if err != nil {
		return agentruntime.ModelResponse{}, err
	}
	publicBlockObserved := false
	var publicBlocks runnerLanguageProgressBlocks
	var publicText strings.Builder
	collect := func(event agentruntime.ModelStreamEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if handled, blockErr := publicBlocks.accept(event); handled || blockErr != nil {
			if blockErr != nil {
				return blockErr
			}
			if event.Kind == agentruntime.ModelStreamEventPublicProgressBoundary {
				publicBlockObserved = true
				block := publicBlocks.closed[len(publicBlocks.closed)-1]
				text := sessionRunnerPublicProgressNarration(block.text)
				if text == "" {
					return nil
				}
				publicText.WriteString(text)
				if client.progressReady != nil {
					client.progressReady("structured")
				}
				if emit != nil {
					if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressDelta, BlockID: block.id, ContentDelta: text}); err != nil {
						return err
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					return emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressBoundary, BlockID: block.id})
				}
			}
			return nil
		}
		// Content remains a private candidate. Forward it incrementally so
		// persistence, cancellation and progress watchdogs retain their cadence.
		// A provisional tool boundary cannot promote that candidate.
		if emit != nil && event.Kind != agentruntime.ModelStreamEventToolCallBoundary {
			return emit(event)
		}
		return nil
	}
	response, err := agentruntime.CompletePublicProgressStream(ctx, client.delegate, projected, collect, client.presentationDiagnostic)
	if err != nil {
		return response, err
	}
	progress, blockID := extractRunnerResponseProgress(&response)
	if publicBlockObserved {
		// The independent block is already authoritative for progress. Strip
		// legacy fields for compatibility, but never republish a second source.
		// Align the tool-round terminal content with the accepted stream, not
		// the decoder's unfiltered text; otherwise downstream language handling
		// could translate or replay a discarded private/overlong narration.
		if len(response.Message.ToolCalls) > 0 {
			response.Message.Content = publicText.String()
		}
		return response, nil
	}
	progressSource := "structured"
	for _, call := range response.Message.ToolCalls {
		if call.ProviderProtocolDiagnostic != "" || (len(call.Arguments) > 0 && !json.Valid(call.Arguments)) {
			// Providers retain malformed proposals for the engine's private
			// protocol repair. A successful transport is not validated arguments.
			return response, nil
		}
	}
	if strings.Contains(response.Message.Content, agentruntime.PublicProgressEnvelopeBegin) {
		return response, nil
	}
	if progress == "" && len(response.Message.ToolCalls) > 0 {
		// A completed native preamble has the same explicit progress role.
		// Preserve it even if later tool admission asks for a private repair,
		// without promoting an earlier interrupted response's candidate bytes.
		progress = sessionRunnerPublicProgressNarration(response.Message.Content)
		blockID = fmt.Sprintf("native-%x", sha256.Sum256([]byte(response.Message.ToolCalls[0].ID+"\x00"+progress)))
		progressSource = "native"
	}
	if progress == "" {
		return response, nil
	}
	if client.progressReady != nil {
		client.progressReady(progressSource)
	}
	// Cadence controls when to request an update, not whether a model-authored
	// finding may be published. The runner's ordinary safety and deduplication
	// boundary remains authoritative; never discard fresh prose because another
	// operation finished recently.
	response.Message.Content = progress
	events := []agentruntime.ModelStreamEvent{
		{Kind: agentruntime.ModelStreamEventToolCallBoundary},
		{Kind: agentruntime.ModelStreamEventPublicProgressDelta, BlockID: blockID, ContentDelta: progress},
		{Kind: agentruntime.ModelStreamEventPublicProgressBoundary, BlockID: blockID},
	}
	for _, event := range events {
		if emit != nil {
			if err := emit(event); err != nil {
				return response, err
			}
		}
	}
	return response, nil
}

func extractRunnerResponseProgress(response *agentruntime.ModelResponse) (string, string) {
	progress, blockID := "", ""
	for index := range response.Message.ToolCalls {
		call := &response.Message.ToolCalls[index]
		var args map[string]json.RawMessage
		if json.Unmarshal(call.Arguments, &args) != nil || args == nil {
			continue
		}
		encoded, exists := args[runnerPublicProgressField]
		if !exists {
			continue
		}
		delete(args, runnerPublicProgressField)
		call.Arguments, _ = json.Marshal(args)
		var text string
		if json.Unmarshal(encoded, &text) != nil || progress != "" {
			continue
		}
		text = strings.TrimSpace(text)
		if safe := sessionRunnerPublicProgressNarration(text); safe != "" {
			progress = safe
			blockID = fmt.Sprintf("response-%x", sha256.Sum256([]byte(call.ID+"\x00"+safe)))
		}
	}
	return progress, blockID
}
