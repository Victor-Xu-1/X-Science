package server

import (
	"context"
	"encoding/json"
	"time"
	"x-science/internal/agentruntime"
	"x-science/internal/harnesscontract"
)

// Observation only: one delegate call, no generated prose, no stream writes,
// no tool decisions. Publication remains owned by the existing runner boundary.
type sessionRunnerCommunicationObserver struct {
	delegate         agentruntime.ModelClient
	publicationBytes func() int
	audit            func(map[string]any)
	pending          map[string]any
	before           int
	now              func() time.Time
	timing           *runnerCommunicationTiming
	auxiliary        bool
}

func (observer *sessionRunnerCommunicationObserver) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	return observer.delegate.Complete(ctx, request)
}

func (observer *sessionRunnerCommunicationObserver) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	if ctx.Value(auxiliaryContextUsageKey{}) == true {
		// A presentation conversion can occur after the primary response but
		// before its publication. Observe that call independently without
		// replacing the still-pending primary request's timing or byte scope.
		auxiliary := &sessionRunnerCommunicationObserver{
			delegate: observer.delegate, audit: observer.audit, now: observer.now, auxiliary: true,
		}
		response, err := auxiliary.completeStream(ctx, request, emit)
		auxiliary.record("presentation_candidate")
		return response, err
	}
	return observer.completeStream(ctx, request, emit)
}

func (observer *sessionRunnerCommunicationObserver) completeStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	observer.record("private_repair")
	observer.before = observer.publishedBytes()
	observer.timing = newRunnerCommunicationTiming(observer.now)
	streamBytes, private := 0, false
	streaming, ok := observer.delegate.(agentruntime.StreamingModelClient)
	var response agentruntime.ModelResponse
	var err error
	if !ok {
		response, err = observer.delegate.Complete(ctx, request)
	} else {
		response, err = streaming.CompleteStream(ctx, request, func(event agentruntime.ModelStreamEvent) error {
			streamBytes += len(event.ContentDelta)
			private = private || event.ReasoningActive || event.Kind == agentruntime.ModelStreamEventPrivateReasoning
			if !event.ReasoningActive && (event.Kind == agentruntime.ModelStreamEventContentDelta || event.Kind == "") && event.ContentDelta != "" {
				observer.timing.noteNativeContent()
			}
			if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
				observer.timing.noteToolBoundary()
			}
			if emit != nil {
				return emit(event)
			}
			return nil
		})
	}
	observer.timing.noteResponseFinished()
	known := map[string]bool{}
	for _, name := range harnesscontract.RootModelTools() {
		known[name] = true
	}
	for _, tool := range request.Tools {
		known[tool.Name] = true
	}
	names := make([]string, 0, len(response.Message.ToolCalls))
	for _, call := range response.Message.ToolCalls {
		if known[call.Name] {
			names = append(names, call.Name)
		} else {
			names = append(names, "unadvertised")
		}
	}
	choice := mapValue(request.ToolChoice)
	structuredBytes := 0
	for _, call := range response.Message.ToolCalls {
		var args map[string]json.RawMessage
		var progress string
		if json.Unmarshal(call.Arguments, &args) == nil && json.Unmarshal(args[runnerPublicProgressField], &progress) == nil {
			structuredBytes += len(progress)
		}
	}
	observer.pending = map[string]any{
		"structured_progress_bytes": structuredBytes,
		"native_text_bytes":         len(response.Message.Content), "native_stream_bytes": streamBytes,
		"safe_native_text_bytes":     len(sessionRunnerPublicProgressNarration(response.Message.Content)),
		"private_reasoning_observed": private, "tool_names": names,
		"required_tool_choice": agentruntime.InitialToolChoiceRequiresCall(request.ToolChoice),
		"required_tool_name":   firstNonEmpty(stringValue(choice["name"]), stringValue(mapValue(choice["function"])["name"])),
	}
	observer.pending["request_scope"] = "agent"
	if observer.auxiliary {
		observer.pending["request_scope"] = "presentation"
	}
	addSessionRunnerNarrationCounters(observer.pending, "native_", response.Message.Content)
	if err != nil {
		observer.record("provider_interrupted")
	}
	return response, err
}

func (observer *sessionRunnerCommunicationObserver) recordBoundary(tools bool) {
	if tools {
		observer.record("tool_boundary")
	} else {
		observer.record("final_candidate")
	}
}

func (observer *sessionRunnerCommunicationObserver) record(decision string) {
	if observer == nil || observer.pending == nil {
		return
	}
	observer.pending["decision"] = decision
	observer.pending["published_bytes"] = observer.publishedBytes() - observer.before
	if decision == "private_repair" && observer.publishedBytes() > observer.before {
		observer.pending["decision"] = "public_preamble_private_tool_repair"
	}
	observer.timing.addTo(observer.pending)
	if observer.audit != nil {
		observer.audit(observer.pending)
	}
	observer.pending = nil
	observer.timing = nil
}

func (observer *sessionRunnerCommunicationObserver) publishedBytes() int {
	if observer == nil || observer.publicationBytes == nil {
		return 0
	}
	return observer.publicationBytes()
}
