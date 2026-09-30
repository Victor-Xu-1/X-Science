package server

import (
	"context"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
)

func TestResponseLanguageFinalDistinguishesStructuredLiteralsFromNarration(t *testing.T) {
	for _, text := range []string{
		"TRACE-CHECK-a18f439b：2+2=4",
		"RUN-CHECK-9f01b2c3; STAT6; IC50=12.5",
		"job_result_alpha: result.csv https://example.invalid/results/42",
	} {
		if sessionRunnerClearlyEnglishNarrative(text, true) {
			t.Errorf("structured literal-only response treated as narration: %q", text)
		}
	}
	for _, text := range []string{
		"THE RESULTS ARE READY",
		"The results in result.csv are ready for review.",
		"Evidence-based analysis remains necessary before completion.",
	} {
		if !sessionRunnerClearlyEnglishNarrative(text, true) {
			t.Errorf("ordinary English narration escaped validation: %q", text)
		}
	}
}

func TestResponseLanguageKeepsExactLiteralInStreamingAndBlockingResponses(t *testing.T) {
	const content = "TRACE-CHECK-a18f439b：2+2=4"
	for _, streaming := range []bool{false, true} {
		model := &responseLanguageSequenceModel{
			chunks:    [][]string{{"TRACE", "-CHECK", "-a18f439b", "：2+2=4"}},
			responses: []agentruntime.ModelResponse{{Message: agentruntime.Message{Role: "assistant", Content: content}}},
		}
		client := &sessionRunnerResponseLanguageModelClient{delegate: model, language: "zh"}
		request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "请原样回复指定标记，不要翻译或扩写。"}}}
		var output strings.Builder
		var response agentruntime.ModelResponse
		var err error
		if streaming {
			response, err = client.CompleteStream(context.Background(), request, func(event agentruntime.ModelStreamEvent) error {
				output.WriteString(event.ContentDelta)
				return nil
			})
		} else {
			response, err = client.Complete(context.Background(), request)
		}
		if err != nil || response.Message.Content != content || len(model.requests) != 1 || streaming && output.String() != content {
			t.Fatalf("streaming=%t calls=%d response=%q published=%q error=%v", streaming, len(model.requests), response.Message.Content, output.String(), err)
		}
	}
}

func TestResponseLanguageStructuredIdentifiersRemainProtectedDuringConversion(t *testing.T) {
	const original = "The result for RUN-CHECK-a18f439b is in job_result_alpha."
	const translated = "RUN-CHECK-a18f439b 的结果是 job_result_alpha。"
	if !responseLanguageLiteralsPreserved(original, translated) {
		t.Fatal("unchanged identifiers rejected")
	}
	for _, altered := range []string{
		strings.ReplaceAll(translated, "a18f439b", "b18f439b"),
		strings.ReplaceAll(translated, "job_result_alpha", "job_result_beta"),
	} {
		if responseLanguageLiteralsPreserved(original, altered) {
			t.Fatalf("identifier mutation accepted: %q", altered)
		}
	}
}
