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
		return "A public progress update is due. First emit one brief, complete public progress envelope describing the immediate next action and its purpose, then continue the work. Do not delay this update to plan later stages or prepare tool arguments."
	}
	return "An additional public progress update is optional. Use a public progress envelope only for materially new progress; do not repeat an unchanged update for parallel calls."
}

// Cadence is request guidance, not a second tool schema or execution gate.
// The public envelope contract has one shared instruction authority. Keep the
// former tool field reserved only so legacy replay stripping cannot remove a
// real executable argument with the same name.
func projectRunnerResponseContract(request agentruntime.ModelRequest, due bool) (agentruntime.ModelRequest, error) {
	if len(request.Tools) == 0 {
		// Translation and other presentation-only calls have their own exact
		// output contract. Do not ask them to plan actions or emit progress.
		return request, nil
	}
	for _, tool := range request.Tools {
		properties, _ := tool.Parameters["properties"].(map[string]any)
		if _, collision := properties[runnerPublicProgressField]; collision {
			return request, fmt.Errorf("tool %q reserves the primary response progress field", tool.Name)
		}
	}
	request.Messages = append([]agentruntime.Message(nil), request.Messages...)
	for index := range request.Messages {
		message := &request.Messages[index]
		if message.Role == "system" && strings.Contains(message.Content, sessionRunnerPublicCommunicationContract) {
			message.Content += "\n\n" + runnerPublicProgressCadence(due)
			return request, nil
		}
	}
	// Keep guidance in the leading system block. Appending it after the latest
	// user/tool message changes provider conversation and recovery semantics.
	boundary := 0
	for boundary < len(request.Messages) && request.Messages[boundary].Role == "system" {
		boundary++
	}
	request.Messages = append(request.Messages, agentruntime.Message{})
	copy(request.Messages[boundary+1:], request.Messages[boundary:])
	request.Messages[boundary] = agentruntime.Message{
		Role: "system", Content: runnerPublicProgressCadence(due), ContextUsageSource: agentruntime.ContextUsageSystemPrompt,
	}
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
// A separately identified native assistant prefix may use the same publication
// path. Ordinary drafts, provisional tool markers and legacy tool-field narration
// still require the complete response; partial JSON never releases an action.
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
	var nativePrefix runnerNativePreamble
	emitPublicBlock := func(id, text, source string) error {
		text = sessionRunnerPublicProgressNarration(text)
		if text == "" {
			return nil
		}
		publicText.WriteString(text)
		if client.progressReady != nil {
			client.progressReady(source)
		}
		if emit == nil {
			return nil
		}
		if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressDelta, BlockID: id, ContentDelta: text}); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventPublicProgressBoundary, BlockID: id})
	}
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
				return emitPublicBlock(block.id, block.text, "structured")
			}
			return nil
		}
		if event.Kind == agentruntime.ModelStreamEventNativePreambleBoundary {
			if publicBlockObserved {
				return nil // An existing independent block is the sole public source.
			}
			publicBlockObserved = true
			text := nativePrefix.narration()
			id := fmt.Sprintf("native-%x", sha256.Sum256([]byte(event.BlockID+"\x00"+text)))
			return emitPublicBlock(id, text, "native")
		}
		if event.Kind == agentruntime.ModelStreamEventContentDelta && !publicBlockObserved {
			nativePrefix.append(event.ContentDelta)
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
