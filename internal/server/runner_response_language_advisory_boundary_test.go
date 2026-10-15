package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

type finalLanguageBoundaryModel struct {
	calls int
	reply func(int) (agentruntime.ModelResponse, error)
}

func (model *finalLanguageBoundaryModel) Complete(_ context.Context, _ agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.calls++
	return model.reply(model.calls)
}

func (model *finalLanguageBoundaryModel) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	response, err := model.Complete(ctx, request)
	if err == nil && emit != nil && response.Message.Content != "" {
		err = emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventContentDelta, ContentDelta: response.Message.Content})
	}
	return response, err
}

func TestFinalLanguageAdvisoryKeepsPrimaryCancellationAndPublicationErrorsHard(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, stage := range []string{"primary_error", "parent_canceled", "empty_primary"} {
			t.Run(fmt.Sprintf("%s/streaming=%t", stage, streaming), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cause := errors.New("primary transport failed")
				model := &finalLanguageBoundaryModel{reply: func(call int) (agentruntime.ModelResponse, error) {
					response := agentruntime.ModelResponse{Message: agentruntime.Message{Content: languageAdvisoryOriginal}}
					if stage == "primary_error" {
						return response, cause // Partial primary bytes are not a completed answer.
					}
					if stage == "empty_primary" {
						return agentruntime.ModelResponse{}, nil
					}
					if call > 1 {
						cancel()
						return agentruntime.ModelResponse{}, errors.New("secondary failed after cancellation")
					}
					return response, nil
				}}
				advisories := 0
				client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh", audit: func(record map[string]any) {
					if record["decision"] == "final_language_advisory" {
						advisories++
					}
				}}
				var response agentruntime.ModelResponse
				var err error
				if streaming {
					response, err = client.CompleteStream(ctx, agentruntime.ModelRequest{}, nil)
				} else {
					response, err = client.Complete(ctx, agentruntime.ModelRequest{})
				}
				if stage == "parent_canceled" {
					cause = context.Canceled
				} else if stage == "empty_primary" {
					cause = nil // Keep the empty response for the existing completion validator.
				}
				if !errors.Is(err, cause) || response.Message.Content != "" || advisories != 0 || model.calls > 2 {
					t.Fatalf("hard boundary softened: response=%q calls=%d audit=%d err=%v", response.Message.Content, model.calls, advisories, err)
				}
			})
		}
	}
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("publication/prefix=%t", published), func(t *testing.T) {
			primary := languageAdvisoryOriginal
			chunks := []string{primary}
			if published {
				primary = "答：" + primary
				chunks = []string{"答：", languageAdvisoryOriginal}
			}
			model := &responseLanguageSequenceModel{chunks: [][]string{chunks}, responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Content: primary}}}}
			client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
			cause := errors.New("durable final persistence failed")
			response, err := client.CompleteStream(context.Background(), agentruntime.ModelRequest{}, func(event agentruntime.ModelStreamEvent) error {
				if strings.Contains(event.ContentDelta, finalLanguageNoticeChinese) {
					return cause
				}
				return nil
			})
			if !errors.Is(err, cause) || response.Message.Content != "" {
				t.Fatalf("publication failure became success: response=%q err=%v", response.Message.Content, err)
			}
		})
	}
}

func TestFinalLanguageAuxiliaryErrorsPreserveMeasuredUsageAndTypedAudit(t *testing.T) {
	for _, tc := range []struct {
		code  string
		cause error
	}{
		{"provider_error", errors.New("secondary transport failed")},
		{"provider_canceled", context.Canceled},
		{"provider_deadline_exceeded", context.DeadlineExceeded},
	} {
		for _, bound := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bound=%t", tc.code, bound), func(t *testing.T) {
				model := &finalLanguageBoundaryModel{reply: func(call int) (agentruntime.ModelResponse, error) {
					if call == 1 {
						return agentruntime.ModelResponse{Message: agentruntime.Message{Content: languageAdvisoryOriginal}, Usage: agentruntime.ModelUsage{TotalTokens: 7}}, nil
					}
					if bound && call == 2 {
						return agentruntime.ModelResponse{Message: agentruntime.Message{Content: "已保存 99 条记录。"}, Usage: agentruntime.ModelUsage{TotalTokens: 5}}, nil
					}
					return agentruntime.ModelResponse{Usage: agentruntime.ModelUsage{TotalTokens: 4}}, tc.cause
				}}
				var advisory map[string]any
				client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh", audit: func(record map[string]any) {
					if record["decision"] == "final_language_advisory" {
						advisory = record
					}
				}}
				response, err := client.Complete(context.Background(), agentruntime.ModelRequest{})
				wantTokens, wantCalls := 11, 2
				if bound {
					wantTokens, wantCalls = 16, 3
				}
				if err != nil || response.Usage.TotalTokens != wantTokens || model.calls != wantCalls || advisory["failure_code"] != tc.code ||
					response.Message.Content != languageAdvisoryOriginal+"\n\n> "+finalLanguageNoticeChinese {
					t.Fatalf("auxiliary identity/usage lost: calls=%d response=%#v audit=%v err=%v", model.calls, response, advisory, err)
				}
			})
		}
	}
}

func TestFinalLanguageAdvisoryDoesNotReplayCompletedTool(t *testing.T) {
	model := &responseLanguageSequenceModel{chunks: [][]string{nil, {languageAdvisoryOriginal}}, responses: []agentruntime.ModelResponse{
		{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "original-action", Name: "inspect", Arguments: json.RawMessage(`{"path":"result.csv"}`)}}}},
		{Message: agentruntime.Message{Role: "assistant", Content: languageAdvisoryOriginal}},
		{Message: agentruntime.Message{Content: "转换完成。", ToolCalls: []agentruntime.ToolCall{{ID: "injected", Name: "inspect"}}}},
	}}
	client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
	calls := 0
	engine := agentruntime.Engine{Model: client, Tools: agentruntime.FuncToolGateway(func(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
		calls++
		if call.ID != "original-action" || string(call.Arguments) != `{"path":"result.csv"}` {
			t.Fatalf("translator changed the action: %#v", call)
		}
		return agentruntime.ToolResult{Value: map[string]any{"ok": true}}, nil
	})}
	result, err := engine.Run(context.Background(), agentruntime.RunRequest{
		Messages:      []agentruntime.Message{{Role: "user", Content: "检查已有文件并汇总"}},
		Tools:         []agentruntime.ToolSchema{{Name: "inspect", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "additionalProperties": false}}},
		MaxToolRounds: 2,
	})
	if err != nil || calls != 1 || len(model.requests) != 4 || result.FinalMessage.Content != languageAdvisoryOriginal+"\n\n> "+finalLanguageNoticeChinese {
		t.Fatalf("final presentation restarted work: calls=%d model=%d final=%q err=%v", calls, len(model.requests), result.FinalMessage.Content, err)
	}
}

func TestFinalLanguageNoticeIsIdempotentAndUsesExistingLanguagePreference(t *testing.T) {
	for _, language := range []string{"", "en", "zh", "unknown"} {
		t.Run(language, func(t *testing.T) {
			audits := 0
			client := &sessionRunnerResponseLanguageModelClient{language: language, audit: func(map[string]any) { audits++ }}
			original := agentruntime.ModelResponse{Message: agentruntime.Message{Content: languageAdvisoryOriginal}}
			response := client.preserveFinalLanguage(original, "provider_error")
			response = client.preserveFinalLanguage(response, "provider_error")
			notice := finalLanguageNoticeEnglish
			if language == "zh" {
				notice = finalLanguageNoticeChinese
			}
			if response.Message.Content != languageAdvisoryOriginal+"\n\n> "+notice || audits != 1 {
				t.Fatalf("duplicate/wrong-locale notice: audits=%d final=%q", audits, response.Message.Content)
			}
		})
	}
}

func TestResponseLanguageFailedProgressKeepsMeasuredAuxiliaryUsage(t *testing.T) {
	for _, action := range []bool{false, true} {
		t.Run(fmt.Sprintf("action=%t", action), func(t *testing.T) {
			model := &responseLanguageSequenceModel{responses: []agentruntime.ModelResponse{{
				Message: agentruntime.Message{Content: "已保存 99 条记录。"}, Usage: agentruntime.ModelUsage{TotalTokens: 5},
			}}}
			client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
			original := agentruntime.ModelResponse{Message: agentruntime.Message{Content: "这是独立的完整答复。"}, Usage: agentruntime.ModelUsage{TotalTokens: 7}}
			wantTokens := 17 // Existing faithful conversion plus the bound route.
			if action {
				original.Message.ToolCalls = []agentruntime.ToolCall{{ID: "original", Name: "inspect"}}
				wantTokens = 12 // Action narration uses only the first conversion.
			}
			blocks := runnerLanguageProgressBlocks{closed: []runnerLanguageProgressBlock{{id: "progress", text: languageAdvisoryOriginal}}}
			response, _, err := client.localizeProgressBlocks(context.Background(), agentruntime.ModelRequest{}, original, blocks, nil)
			if err != nil || response.Usage.TotalTokens != wantTokens || response.Message.Content != original.Message.Content || len(response.Message.ToolCalls) != len(original.Message.ToolCalls) {
				t.Fatalf("discarded optional conversion lost usage or affected final/action: response=%#v err=%v", response, err)
			}
		})
	}
}
