package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"x-science/internal/providers"
)

// Codes, not provider messages or draft contents, cross the UI/log boundary.
type conversationSkillFailureCode string

const (
	skillAuthoringTimeout             conversationSkillFailureCode = "skill_authoring_timeout"
	skillAuthoringCancelled           conversationSkillFailureCode = "skill_authoring_cancelled"
	skillAuthoringOutputLimit         conversationSkillFailureCode = "skill_authoring_output_limit"
	skillAuthoringRefused             conversationSkillFailureCode = "skill_authoring_refused"
	skillAuthoringInvalidDraft        conversationSkillFailureCode = "skill_authoring_invalid_draft"
	skillAuthoringProviderAuth        conversationSkillFailureCode = "skill_authoring_provider_auth"
	skillAuthoringRateLimited         conversationSkillFailureCode = "skill_authoring_rate_limited"
	skillAuthoringProviderUnavailable conversationSkillFailureCode = "skill_authoring_provider_unavailable"
	skillAuthoringConfiguration       conversationSkillFailureCode = "skill_authoring_configuration"
	skillAuthoringSourceUnavailable   conversationSkillFailureCode = "skill_authoring_source_unavailable"
)

type conversationSkillAuthoringFailure struct {
	code  conversationSkillFailureCode
	cause error
}

func (failure *conversationSkillAuthoringFailure) Error() string { return string(failure.code) }
func (failure *conversationSkillAuthoringFailure) Unwrap() error { return failure.cause }

func conversationSkillFailure(err error, fallback conversationSkillFailureCode) error {
	if err == nil {
		return nil
	}
	var timeout net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		fallback = skillAuthoringTimeout
	case errors.Is(err, context.Canceled):
		fallback = skillAuthoringCancelled
	case errors.As(err, &timeout) && timeout.Timeout():
		fallback = skillAuthoringTimeout
	case providers.IsProviderOutputTokenLimit(err):
		fallback = skillAuthoringOutputLimit
	default:
		if status, found := providers.HTTPStatus(err); found {
			fallback = conversationSkillHTTPFailureCode(status)
		}
	}
	return &conversationSkillAuthoringFailure{code: fallback, cause: err}
}

func conversationSkillHTTPFailureCode(status int) conversationSkillFailureCode {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return skillAuthoringProviderAuth
	case http.StatusTooManyRequests:
		return skillAuthoringRateLimited
	default:
		return skillAuthoringProviderUnavailable
	}
}

func conversationSkillErrorCode(err error) conversationSkillFailureCode {
	var failure *conversationSkillAuthoringFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return skillAuthoringProviderUnavailable
}

type conversationSkillOperationContextKey struct{}

func withConversationSkillOperation(ctx context.Context, operation string) context.Context {
	if len(operation) == 0 || len(operation) > 128 {
		return ctx
	}
	for _, character := range operation {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return ctx
		}
	}
	return context.WithValue(ctx, conversationSkillOperationContextKey{}, operation)
}

// This projection intentionally has no free-form error, model reply, endpoint,
// headers, credential, transcript or executable-content field.
type conversationSkillDiagnostic struct {
	Operation             string                       `json:"operation,omitempty"`
	Stage                 string                       `json:"stage"`
	Code                  conversationSkillFailureCode `json:"code,omitempty"`
	Attempt               int                          `json:"attempt,omitempty"`
	HTTPStatus            int                          `json:"http_status,omitempty"`
	DurationMS            int64                        `json:"duration_ms,omitempty"`
	RequestedOutputTokens int                          `json:"requested_output_tokens,omitempty"`
	CompletionTokens      int                          `json:"completion_tokens,omitempty"`
	ValidationReason      conversationSkillDraftReason `json:"validation_reason,omitempty"`
	ReturnCarrier         string                       `json:"return_carrier,omitempty"`
	ToolCallCount         int                          `json:"tool_call_count,omitempty"`
	ContentBytes          int                          `json:"content_bytes,omitempty"`
	ArgumentBytes         int                          `json:"argument_bytes,omitempty"`
	At                    time.Time                    `json:"at"`
}

func logConversationSkillDiagnostic(ctx context.Context, event conversationSkillDiagnostic) {
	event.Operation, _ = ctx.Value(conversationSkillOperationContextKey{}).(string)
	event.At = time.Now().UTC()
	encoded, err := json.Marshal(event)
	if err == nil {
		log.Printf("conversation_skill_authoring %s", encoded)
	}
}

func conversationSkillModelDiagnostic(record providers.AuditRecord) conversationSkillDiagnostic {
	event := conversationSkillDiagnostic{Stage: "model_call", Attempt: record.Attempt,
		HTTPStatus: record.HTTPStatus, DurationMS: record.DurationMs,
		RequestedOutputTokens: record.RequestedOutputTokens, CompletionTokens: record.CompletionTokens}
	switch {
	case record.OutputTokenLimited:
		event.Code = skillAuthoringOutputLimit
	case record.HTTPStatus >= 400:
		event.Code = conversationSkillHTTPFailureCode(record.HTTPStatus)
	case record.Error != "":
		event.Code = skillAuthoringProviderUnavailable
	}
	return event
}
