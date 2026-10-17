package server

import (
	"errors"
	"strings"

	"x-science/internal/agentruntime"
)

type conversationSkillDraftReason string

const (
	skillDraftMissingReturn     conversationSkillDraftReason = "missing_return"
	skillDraftAmbiguousReturn   conversationSkillDraftReason = "ambiguous_return"
	skillDraftUnexpectedReturn  conversationSkillDraftReason = "unexpected_return"
	skillDraftProviderArguments conversationSkillDraftReason = "provider_arguments"
	skillDraftJSONContract      conversationSkillDraftReason = "json_contract"
	skillDraftManifestContract  conversationSkillDraftReason = "manifest_contract"
)

type conversationSkillDraftValidationError struct {
	reason conversationSkillDraftReason
	cause  error
}

func (failure *conversationSkillDraftValidationError) Error() string {
	return "Skill draft failed validation: " + string(failure.reason)
}

func (failure *conversationSkillDraftValidationError) Unwrap() error { return failure.cause }

func invalidConversationSkillDraft(reason conversationSkillDraftReason, cause error) error {
	return &conversationSkillDraftValidationError{reason: reason, cause: cause}
}

// Carrier compatibility does not relax the draft schema. A tool return is
// authoritative when present; text is eligible only with no tool calls and
// must be the whole strict JSON object, never extracted or repaired from prose.
func conversationSkillDraftFromResponse(response agentruntime.ModelResponse, requestedName string) (conversationSkillDraft, error) {
	calls := response.Message.ToolCalls
	var raw string
	switch len(calls) {
	case 0:
		raw = response.Message.Content
		if strings.TrimSpace(raw) == "" {
			return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftMissingReturn, nil)
		}
	case 1:
		if calls[0].Name != conversationSkillDraftReturnTool {
			return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftUnexpectedReturn, nil)
		}
		if strings.TrimSpace(calls[0].ProviderProtocolDiagnostic) != "" {
			return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftProviderArguments, nil)
		}
		raw = string(calls[0].Arguments)
	default:
		return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftAmbiguousReturn, nil)
	}
	draft, err := decodeConversationSkillDraft(raw)
	if err != nil {
		return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftJSONContract, err)
	}
	if requestedName != "" {
		draft.Name = requestedName
	}
	markdown, err := normalizeConversationSkillManifest(draft.SkillMarkdown, draft.Name, draft.Description)
	if err != nil {
		return conversationSkillDraft{}, invalidConversationSkillDraft(skillDraftManifestContract, err)
	}
	draft.SkillMarkdown = markdown
	return draft, nil
}

// Only closed categories and byte/count metadata cross the diagnostic boundary.
// No returned name, content, arguments, protocol diagnostic or raw error does.
func conversationSkillDraftDiagnostic(response agentruntime.ModelResponse, attempt int, err error) conversationSkillDiagnostic {
	event := conversationSkillDiagnostic{Stage: "draft_validated", Attempt: attempt,
		ToolCallCount: len(response.Message.ToolCalls), ContentBytes: len(response.Message.Content)}
	switch len(response.Message.ToolCalls) {
	case 0:
		event.ReturnCarrier = "whole_json"
		if event.ContentBytes == 0 {
			event.ReturnCarrier = "none"
		}
	case 1:
		event.ReturnCarrier = "tool_call"
		event.ArgumentBytes = len(response.Message.ToolCalls[0].Arguments)
	default:
		event.ReturnCarrier = "ambiguous"
	}
	if err != nil {
		event.Stage = "draft_validation"
		event.Code = skillAuthoringInvalidDraft
		var failure *conversationSkillDraftValidationError
		if errors.As(err, &failure) {
			event.ValidationReason = failure.reason
		}
	}
	return event
}
