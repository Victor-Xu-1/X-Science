package server

import (
	"context"
	"errors"
	"strings"

	"x-science/internal/agentruntime"
)

func (client *sessionRunnerResponseLanguageModelClient) discardProgressPresentation(err error) {
	code := "invalid_progress"
	if failure, ok := err.(sessionRunnerPresentationViolation); ok {
		code = failure.Code
	}
	if client.audit != nil {
		client.audit(map[string]any{"decision": "progress_presentation_discarded", "failure_code": code})
	}
}

func (client *sessionRunnerResponseLanguageModelClient) localizeProgressBlocks(ctx context.Context, request agentruntime.ModelRequest, response agentruntime.ModelResponse, blocks runnerLanguageProgressBlocks, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, bool, error) {
	if blocks.open != nil {
		client.discardProgressPresentation(sessionRunnerPresentationViolation{Code: "incomplete_block"})
		return response, false, nil
	}
	if len(blocks.closed) == 0 {
		return response, false, nil
	}
	var originalText, localizedText strings.Builder
	for _, block := range blocks.closed {
		text := block.text
		originalText.WriteString(text)
		if block.forwarded {
			localizedText.WriteString(block.forwardedText)
			continue
		}
		if sessionRunnerRequiresChinese(client.language) && sessionRunnerClearlyEnglishProgress(text) {
			candidate := agentruntime.ModelResponse{Message: agentruntime.Message{Content: text, ToolCalls: response.Message.ToolCalls}}
			translated, err := client.localizeOrKeepToolCall(ctx, request, candidate)
			if err != nil {
				if ctx.Err() != nil {
					return response, false, ctx.Err()
				}
				if errors.Is(err, context.Canceled) {
					return response, false, err
				}
				// A separate valid final answer or original action must not be
				// rejected just because optional progress conversion was unavailable.
				client.discardProgressPresentation(sessionRunnerPresentationViolation{Code: "conversion_unavailable"})
				text = ""
			} else {
				text = translated.Message.Content
				response.Usage = addModelUsage(response.Usage, translated.Usage)
			}
		}
		localizedText.WriteString(text)
		if err := client.emitProgressBlock(ctx, block.id, text, emit); err != nil {
			return response, false, err
		}
	}
	// The response-contract adapter selects this progress as canonical tool-round
	// content. Any discarded native report draft must not be translated/published
	// again, nor should the canonical event reintroduce the original English.
	if len(response.Message.ToolCalls) > 0 && response.Message.Content == originalText.String() {
		response.Message.Content = localizedText.String()
		return response, true, nil
	}
	return response, false, nil
}

// A complete public block has an independent publication boundary. Pass blocks
// already in the requested language immediately; any optional translation still
// waits for the complete native action so it cannot rewrite partial tool data.
func (client *sessionRunnerResponseLanguageModelClient) forwardReadyProgressBlocks(ctx context.Context, blocks *runnerLanguageProgressBlocks, emit func(agentruntime.ModelStreamEvent) error) error {
	for index := range blocks.closed {
		block := &blocks.closed[index]
		if block.forwarded {
			continue
		}
		if sessionRunnerRequiresChinese(client.language) && sessionRunnerClearlyEnglishProgress(block.text) {
			break // Preserve order behind the earlier block awaiting conversion.
		}
		text := sessionRunnerPublicProgressNarration(block.text)
		if err := client.emitProgressBlock(ctx, block.id, text, emit); err != nil {
			return err
		}
		block.forwarded, block.forwardedText = true, text
	}
	return nil
}

func (client *sessionRunnerResponseLanguageModelClient) emitProgressBlock(ctx context.Context, id, text string, emit func(agentruntime.ModelStreamEvent) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if emit == nil || text == "" {
		return nil
	}
	if client.presentationReady != nil {
		client.presentationReady()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressDelta, BlockID: id, ContentDelta: text}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressBoundary, BlockID: id})
}
