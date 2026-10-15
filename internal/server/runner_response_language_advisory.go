package server

import (
	"context"
	"errors"
	"strings"

	"x-science/internal/agentruntime"
)

const (
	finalLanguageNoticeEnglish = "The response does not fully match the preferred language. The original answer is preserved; an unreliable translation has not replaced it."
	finalLanguageNoticeChinese = "语言呈现未完全符合当前偏好。已保留原始答复，未用不可靠的翻译替换原文。"
)

// Only a complete, nonempty primary final has this authority. Tool/progress
// narration retains its separate existing boundary, and the translator can
// neither replace an action nor turn an empty primary response into an answer.
func (client *sessionRunnerResponseLanguageModelClient) localizePrimaryResponse(ctx context.Context, request agentruntime.ModelRequest, original agentruntime.ModelResponse) (agentruntime.ModelResponse, error) {
	if len(original.Message.ToolCalls) > 0 {
		return client.localizeOrKeepToolCall(ctx, request, original)
	}
	translated, err := client.translateCandidate(ctx, request, original)
	if ctx.Err() != nil {
		return agentruntime.ModelResponse{}, ctx.Err()
	}
	if err == nil {
		return translated, nil
	}
	if strings.TrimSpace(original.Message.Content) == "" {
		return agentruntime.ModelResponse{}, err
	}
	// Failed conversions return the untouched primary response with measured
	// auxiliary usage accumulated. No failed/partial translator text is used.
	original.Usage = translated.Usage
	code := "provider_error"
	var mismatch sessionRunnerResponseLanguageMismatch
	switch {
	case errors.As(err, &mismatch):
		code = mismatch.validationCode()
	case errors.Is(err, context.Canceled):
		code = "provider_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "provider_deadline_exceeded"
	}
	return client.preserveFinalLanguage(original, code), nil
}

func (client *sessionRunnerResponseLanguageModelClient) preserveFinalLanguage(original agentruntime.ModelResponse, code string) agentruntime.ModelResponse {
	content := original.Message.Content
	if strings.TrimSpace(content) == "" {
		return original
	}
	notice := finalLanguageNoticeEnglish
	if sessionRunnerRequiresChinese(client.language) {
		notice = finalLanguageNoticeChinese
	}
	if strings.HasSuffix(content, "\n\n> "+finalLanguageNoticeEnglish) || strings.HasSuffix(content, "\n\n> "+finalLanguageNoticeChinese) {
		return original
	}
	original.Message.Content += "\n\n> " + notice
	client.auditConversion(map[string]any{
		"decision": "final_language_advisory", "failure_code": code,
		"input_bytes": len(content), "output_bytes": len(original.Message.Content),
	}, content, original.Message.Content)
	return original
}
