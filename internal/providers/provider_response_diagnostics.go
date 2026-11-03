package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"x-science/internal/agentruntime"
)

type ProviderTerminationReason string
type ProviderResponseOutcome string

// ProviderResponseDiagnostics contains only observed structure, never provider
// text, tool arguments or execution authority. Nil means unobserved; pointers
// retain observed false/zero. Currently only native chat-completions is wired.
type ProviderResponseDiagnostics struct {
	TerminationReason    *ProviderTerminationReason    `json:"terminationReason,omitempty"`
	TerminalObserved     *bool                         `json:"terminalObserved,omitempty"`
	RawToolCallsObserved *bool                         `json:"rawToolCallsObserved,omitempty"`
	RawToolCallCount     *int                          `json:"rawToolCallCount,omitempty"`
	DecodedToolCallCount *int                          `json:"decodedToolCallCount,omitempty"`
	Outcome              *ProviderResponseOutcome      `json:"outcome,omitempty"`
	Incomplete           *bool                         `json:"incomplete,omitempty"`
	Transport            *ProviderTransportDiagnostics `json:"transport,omitempty"`
}

// Each actual HTTP attempt owns its observer. Never put it on the reused
// runtimeModelClient or merge it with a subsequent argument-repair request.
type providerResponseObservation struct {
	value                          ProviderResponseDiagnostics
	sawChoice, sawTool, terminal   bool
	providerError, consumerFailure bool
	protocolFailure                bool
	transport                      *providerTransportObservation
}

func diagnosticPointer[T any](value T) *T { return &value }

func (o *providerResponseObservation) observeChoice(reason string, hasTools bool) {
	if o == nil {
		return
	}
	o.sawChoice = true
	o.sawTool = o.sawTool || hasTools
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return
	}
	switch reason {
	case "stop", "tool_calls", "function_call", "length", "content_filter":
		o.terminal = true
	default:
		reason = "unsupported" // Arbitrary provider strings are not diagnostics.
	}
	o.value.TerminationReason = diagnosticPointer(ProviderTerminationReason(reason))
}

func (o *providerResponseObservation) observeEnvelope(raw json.RawMessage) {
	if o != nil && len(raw) > 0 && strings.TrimSpace(string(raw)) != "null" {
		o.providerError = true
	}
}

func (o *providerResponseObservation) observeJSON(decoded openAIChatResponse) {
	if o == nil {
		return
	}
	o.observeEnvelope(decoded.Error)
	if len(decoded.Choices) == 0 {
		return
	}
	choice := decoded.Choices[0]
	o.observeChoice(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	o.terminal = true // The existing JSON parser received a complete choice.
	o.value.TerminalObserved = diagnosticPointer(true)
	ids := make([]string, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		ids = append(ids, call.ID)
	}
	o.completeRawCalls(ids)
}

func (o *providerResponseObservation) completeRawCalls(ids []string) {
	if !o.terminal || !o.sawChoice || o.providerError {
		return
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return // An ambiguous identity cannot establish a complete count.
		}
		seen[id] = true
	}
	o.value.RawToolCallsObserved = diagnosticPointer(len(ids) > 0)
	o.value.RawToolCallCount = diagnosticPointer(len(ids))
}

func (o *providerResponseObservation) finishStream(a *openAIChatStreamAccumulator, response agentruntime.ModelResponse, err error) {
	if o == nil {
		return
	}
	o.value.TerminalObserved = diagnosticPointer(o.terminal)
	o.value.Incomplete = diagnosticPointer(!o.terminal || IsProviderOutputTokenLimit(err))
	if err == nil {
		ids := make([]string, 0, len(a.toolCalls))
		for _, call := range a.toolCalls {
			ids = append(ids, call.ID)
		}
		o.completeRawCalls(ids)
	}
	o.finishDecode(response, err)
}

func (o *providerResponseObservation) finishDecode(response agentruntime.ModelResponse, err error) {
	if o == nil {
		return
	}
	if response.Message.Content != "" || response.Message.ReasoningContent != "" || len(response.Message.ToolCalls) != 0 {
		// JSON fallback has one completed decoder admission, not incremental
		// transport growth. SSE was observed at its original progress branch.
		o.transport.observeGrowth(1, true)
	}
	o.value.DecodedToolCallCount = diagnosticPointer(len(response.Message.ToolCalls))
	if o.sawTool {
		o.value.RawToolCallsObserved = diagnosticPointer(true)
	}
	if err != nil {
		o.value.RawToolCallCount = nil
	}
	if IsProviderOutputTokenLimit(err) {
		o.value.Incomplete = diagnosticPointer(true)
	} else if o.value.TerminalObserved != nil && o.value.Incomplete == nil {
		o.value.Incomplete = diagnosticPointer(false)
	}
	o.classify(err)
	if err == nil && *o.value.Outcome == "decoded" && hasProviderArgumentDiagnostic(response) {
		o.value.Outcome = diagnosticPointer(ProviderResponseOutcome("protocol_feedback"))
	} else if err == nil && *o.value.Outcome == "decoded" && !response.Refused &&
		len(response.Message.ToolCalls) == 0 && strings.TrimSpace(response.Message.Content) == "" {
		o.value.Outcome = diagnosticPointer(ProviderResponseOutcome("empty_response"))
	}
}

// Classification uses owned branches or typed causes, never error prose.
// Recording a diagnostic does not change the original error or retry policy.
func (o *providerResponseObservation) classify(err error) {
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	outcome := "decoded"
	switch {
	case errors.Is(err, context.Canceled):
		outcome = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		outcome = "deadline"
	case o.consumerFailure:
		outcome = "consumer_error"
	case o.providerError:
		outcome = "provider_error_envelope"
	case IsProviderOutputTokenLimit(err):
		outcome = "output_limited"
	case errors.Is(err, errProviderStreamIncomplete):
		outcome = "incomplete_stream"
	case errors.Is(err, errOpenAIChatStreamRead):
		outcome = "stream_read_error"
	case errors.As(err, &syntax), errors.As(err, &shape):
		outcome = "invalid_json"
	case IsProviderEmptyResponse(err):
		outcome = "empty_response"
	case IsRetryableModelProtocolError(err), o.protocolFailure:
		outcome = "invalid_protocol"
	case err != nil:
		outcome = "unknown_error"
	}
	o.value.Outcome = diagnosticPointer(ProviderResponseOutcome(outcome))
}

// Preserve the exact parser error while recording its owned validation branch.
func (o *providerResponseObservation) protocolError(err error) error {
	if o != nil {
		o.protocolFailure = true
	}
	return err
}

func (o *providerResponseObservation) audit(record AuditRecord, err error) AuditRecord {
	if o == nil {
		return record
	}
	if err != nil {
		// The transport returns no ModelResponse on failure, including typed
		// output limits classified after a successful JSON decode. Preserve
		// unobserved counts and the independent raw native-call observations.
		if o.value.DecodedToolCallCount != nil {
			o.value.DecodedToolCallCount = diagnosticPointer(0)
		}
		if IsProviderOutputTokenLimit(err) {
			o.value.Incomplete = diagnosticPointer(true)
		}
		o.classify(err)
	}
	if record.HTTPStatus >= http.StatusBadRequest {
		o.value.Outcome = diagnosticPointer(ProviderResponseOutcome("http_error"))
	}
	value := o.value // Later request-local changes cannot replace this snapshot.
	value.Transport = o.transport.snapshot(err, value.TerminalObserved)
	record.ResponseDiagnostics = &value
	return record
}

func (o *providerResponseObservation) emit(emit func(agentruntime.ModelStreamEvent) error) func(agentruntime.ModelStreamEvent) error {
	if o == nil {
		return emit
	}
	return func(event agentruntime.ModelStreamEvent) error {
		err := emit(event)
		if err != nil {
			o.consumerFailure = true
		}
		return err
	}
}

func (c *runtimeModelClient) observedOpenAIResponseDecoder(body []byte, headers http.Header, observation *providerResponseObservation) (response agentruntime.ModelResponse, requestID string, usage providerTokenUsage, err error) {
	defer func() { observation.finishDecode(response, err) }()
	var decoded openAIChatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return agentruntime.ModelResponse{}, "", providerTokenUsage{}, err
	}
	observation.observeJSON(decoded)
	usage = decoded.Usage.tokenUsage()
	usage.deriveTotal(false)
	if len(decoded.Choices) == 0 {
		return agentruntime.ModelResponse{}, decoded.ID, usage, observation.protocolError(errors.New("OpenAI-compatible response has no choices"))
	}
	if len(decoded.Choices[0].Message.ToolCalls) > 0 {
		if limited := responseOutputLimit(decoded.Choices[0].FinishReason, true); limited != nil {
			return agentruntime.ModelResponse{}, decoded.ID, usage, limited
		}
	}
	message, err := runtimeMessageFromOpenAI(decoded.Choices[0].Message)
	if err != nil {
		return agentruntime.ModelResponse{}, decoded.ID, usage, err
	}
	refused := strings.TrimSpace(decoded.Choices[0].Message.Refusal) != ""
	if isTokenLimitFinishReason(decoded.Choices[0].FinishReason) {
		if len(message.ToolCalls) != 0 {
			return agentruntime.ModelResponse{}, decoded.ID, usage,
				fmt.Errorf("%w: finish reason %s", errProviderResponseTruncated, decoded.Choices[0].FinishReason)
		}
		// Preserve the existing content-only continuation contract, including an
		// empty prefix; diagnostic observation is not a new completion gate.
		return agentruntime.ModelResponse{
				Message: message, Model: decoded.Model,
				StopReason: decoded.Choices[0].FinishReason, Refused: refused,
			}, firstNonEmpty(decoded.ID, decoded.RequestID), usage,
			newProviderContentResponseTruncation(decoded.Choices[0].FinishReason)
	}
	return agentruntime.ModelResponse{
		Message: message, Model: decoded.Model,
		StopReason: decoded.Choices[0].FinishReason, Refused: refused,
	}, firstNonEmpty(decoded.ID, decoded.RequestID), usage, nil
}
