package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

// This persistence DTO is deliberately not ModelRequest. Headers, Metadata,
// MediaPolicy, hidden tool evidence and live runtime state have no representation.
type memoryForkedRequestView struct {
	Messages              []memoryForkedMessage      `json:"messages"`
	Tools                 []memoryForkedTool         `json:"tools,omitempty"`
	MaxTokens             int                        `json:"max_tokens,omitempty"`
	ProviderDefaultBudget bool                       `json:"provider_default_budget,omitempty"`
	Temperature           *float64                   `json:"temperature,omitempty"`
	ToolChoice            any                        `json:"tool_choice,omitempty"`
	ReasoningMode         agentruntime.ReasoningMode `json:"reasoning_mode,omitempty"`
}

type memoryForkedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// Existing provider prompt content, not a host grant or public transcript.
	ReasoningContent string                     `json:"reasoning_content,omitempty"`
	Parts            []agentruntime.ContentPart `json:"parts,omitempty"`
	ToolCallID       string                     `json:"tool_call_id,omitempty"`
	ToolCalls        []memoryForkedToolCall     `json:"tool_calls,omitempty"`
}

type memoryForkedToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Providers consume this JSON as an argument string. A nested RawMessage
	// would lose original ordering/outer whitespace in outbox canonicalization.
	Arguments string `json:"arguments"`
}

type memoryForkedTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type boundedMemoryViewBuffer struct{ bytes.Buffer }

type memoryViewDiagnostic string

const memoryViewOversize memoryViewDiagnostic = "original-request-view-oversize"

// Only the bounded encoder can issue this proof. A stored diagnostic string or
// an arbitrary MarshalJSON error must never authorize a different extraction.
type memoryViewOversizeError struct{}

func (memoryViewOversizeError) Error() string { return string(memoryViewOversize) }

func (buffer *boundedMemoryViewBuffer) Write(value []byte) (int, error) {
	if len(value) > workspace.MemoryExtractionViewMaxBytes-buffer.Len() {
		return 0, memoryViewOversizeError{}
	}
	return buffer.Buffer.Write(value)
}

func encodeMemoryForkedRequestView(request *agentruntime.ModelRequest) (json.RawMessage, memoryViewDiagnostic) {
	if request == nil {
		return nil, "original-request-view-missing"
	}
	view := memoryForkedRequestView{MaxTokens: request.MaxTokens, ProviderDefaultBudget: request.UseProviderDefaultOutputBudget,
		Temperature: request.Temperature, ToolChoice: request.ToolChoice, ReasoningMode: request.ReasoningMode}
	for _, message := range request.Messages {
		copy := memoryForkedMessage{Role: message.Role, Content: message.Content, ReasoningContent: message.ReasoningContent, ToolCallID: message.ToolCallID}
		for _, part := range message.Parts {
			if part.Type != agentruntime.ContentPartText || part.Media != nil {
				return nil, "original-request-view-unsupported-media"
			}
			copy.Parts = append(copy.Parts, agentruntime.ContentPart{Type: part.Type, Text: part.Text})
		}
		for _, call := range message.ToolCalls {
			if !utf8.Valid(call.Arguments) {
				return nil, "original-request-view-unsupported-json"
			}
			copy.ToolCalls = append(copy.ToolCalls, memoryForkedToolCall{ID: call.ID, Name: call.Name, Arguments: string(call.Arguments)})
		}
		view.Messages = append(view.Messages, copy)
	}
	for _, tool := range request.Tools {
		view.Tools = append(view.Tools, memoryForkedTool{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	var buffer boundedMemoryViewBuffer
	if err := json.NewEncoder(&buffer).Encode(view); err != nil {
		var oversized memoryViewOversizeError
		if errors.As(err, &oversized) {
			return nil, memoryViewOversize
		}
		return nil, "original-request-view-unsupported-json"
	}
	// The existing outbox canonicalizes JSON objects; bind that same exact
	// canonical view, not the incidental field order of this DTO encoder.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	decoder.UseNumber()
	if decoder.Decode(&canonical) != nil {
		return nil, "original-request-view-unsupported-json"
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, "original-request-view-unsupported-json"
	}
	if len(raw) > workspace.MemoryExtractionViewMaxBytes {
		return nil, memoryViewOversize
	}
	return raw, ""
}

func decodeMemoryForkedRequestView(raw json.RawMessage) (*agentruntime.ModelRequest, error) {
	if len(raw) == 0 || len(raw) > workspace.MemoryExtractionViewMaxBytes {
		return nil, errors.New("original-request-view-missing-or-oversize")
	}
	var view memoryForkedRequestView
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&view); err != nil {
		return nil, err
	}
	request := &agentruntime.ModelRequest{MaxTokens: view.MaxTokens, UseProviderDefaultOutputBudget: view.ProviderDefaultBudget,
		Temperature: view.Temperature, ToolChoice: view.ToolChoice, ReasoningMode: view.ReasoningMode}
	for _, message := range view.Messages {
		copy := agentruntime.Message{Role: message.Role, Content: message.Content, ReasoningContent: message.ReasoningContent, ToolCallID: message.ToolCallID}
		for _, part := range message.Parts {
			if part.Type != agentruntime.ContentPartText || part.Media != nil {
				return nil, errors.New("original-request-view-unsupported-media")
			}
			copy.Parts = append(copy.Parts, part)
		}
		for _, call := range message.ToolCalls {
			copy.ToolCalls = append(copy.ToolCalls, agentruntime.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(call.Arguments)})
		}
		request.Messages = append(request.Messages, copy)
	}
	for _, tool := range view.Tools {
		request.Tools = append(request.Tools, agentruntime.ToolSchema{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	return request, nil
}

func memoryViewDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func memoryExtractionPreparationDiagnostic(configMode string, everyN int) *workspace.MemoryExtractionCompletion {
	mode := configMode
	if mode == "haiku" {
		mode = "compact"
	}
	if mode != "compact" && mode != "forked" {
		mode = "forked"
	}
	if everyN < 1 {
		everyN = 1
	}
	return &workspace.MemoryExtractionCompletion{Mode: mode, ExtractEveryN: everyN, Diagnostic: "memory-preparation-unavailable"}
}

func (runtime *memoryExtractionRuntime) prepareCompletedMemoryExtraction(ctx context.Context, authority *transcriptRunnerAuthority) (*workspace.MemoryExtractionCompletion, error) {
	if runtime == nil || authority == nil || runtime.server == nil {
		return nil, nil
	}
	resolved, err := runtime.resolveCompletedRootMemoryScope(ctx, authority.Stream.SessionID)
	if err != nil {
		return nil, err
	}
	if !resolved.Enabled || resolved.Scope.FrameID != resolved.Scope.SourceFrameID || !runtime.server.memoryConfig.ExtractEnabled {
		return nil, nil
	}
	preference, err := runtime.server.memoryAutomaticPreferences(resolved.Scope.UserID)
	if err != nil || !preference.Enabled {
		return nil, err
	}
	value := &workspace.MemoryExtractionCompletion{Mode: runtime.server.memoryConfig.ExtractMode, ExtractEveryN: runtime.server.memoryConfig.ExtractEveryN}
	if value.ExtractEveryN < 1 {
		value.ExtractEveryN = 1
	}
	if value.Mode == "haiku" {
		value.Mode = "compact"
	}
	if value.Mode != "compact" && value.Mode != "forked" {
		value.Mode = "forked"
		value.Diagnostic = "original-extraction-mode-unsupported"
	}
	runtime.mu.Lock()
	var request *agentruntime.ModelRequest
	if state := runtime.sessions[authority.Stream.SessionID]; state != nil && state.requestAttempt == authority.Claim.Attempt && state.request != nil {
		copy := agentruntime.SnapshotModelRequest(*state.request)
		request = &copy
		value.Model = state.model
	}
	runtime.mu.Unlock()
	if value.Mode == "forked" {
		var diagnostic memoryViewDiagnostic
		value.RequestView, diagnostic = encodeMemoryForkedRequestView(request)
		if value.Diagnostic == "" {
			value.Diagnostic = string(diagnostic)
		}
		runtime.prepareMemoryModeDecision(ctx, authority, resolved.Scope, preference, request, diagnostic, value)
	} else if value.Diagnostic == "" {
		runtime.prepareMemoryModeDecision(ctx, authority, resolved.Scope, preference, request, "", value)
	}
	if len(value.RequestView) > 0 {
		value.RequestViewSHA256 = memoryViewDigest(value.RequestView)
	}
	if value.Model.Model == "" && value.Diagnostic == "" {
		value.Diagnostic = "original-model-identity-missing"
	}
	return value, nil
}
