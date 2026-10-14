package providers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

type generationAdmissionBody struct{ *generationProgressReader }

func (body *generationAdmissionBody) Close() error { return nil }

type nativeAdmissionDecoder func(*runtimeModelClient, http.Response, func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, string, providerTokenUsage, bool, error)

func TestProviderIgnoredNativeFramesDoNotRecordDecoderGrowth(t *testing.T) {
	for _, fixture := range []struct {
		name, data string
		decode     nativeAdmissionDecoder
	}{
		{"gemini_ignores_anthropic_frame", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"fixture"}}`, readGeminiStream},
		{"anthropic_ignores_gemini_frame", `{"candidates":[{"content":{"parts":[{"text":"fixture","thought":true}]}}]}`, readAnthropicStream},
		{"anthropic_ignores_unknown_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"unused_fixture","thinking":"fixture","text":"fixture"}}`, readAnthropicStream},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: 4096}}}
			events := 0
			response, _, _, emitted, err := fixture.decode(client, http.Response{Body: &generationAdmissionBody{reader}}, func(agentruntime.ModelStreamEvent) error {
				events++
				return nil
			})
			if err == nil || emitted || events != 0 || reader.observations != 0 || reader.bytes != 0 || response.Message.Content != "" || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("ignored protocol fields acquired activity: err=%v emitted=%t events=%d observations=%d growth=%d", err, emitted, events, reader.observations, reader.bytes)
			}
		})
	}
}

func TestProviderHeaderOnlyNativeGrowthKeepsSemanticByteBound(t *testing.T) {
	const limit = int64(4096)
	frame := "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"" + strings.Repeat("x", 64) + "\"}}\n\n"
	reader := &generationProgressReader{Reader: strings.NewReader(strings.Repeat(frame, 100))}
	client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: limit}}}
	public := false
	_, _, _, emitted, err := readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, func(event agentruntime.ModelStreamEvent) error {
		public = public || event.ContentDelta != ""
		return nil
	})
	if !errors.Is(err, errProviderResponseTooLarge) || emitted || public || reader.bytes > limit {
		t.Fatalf("header-only private growth escaped semantic budget: err=%v emitted=%t public=%t growth=%d limit=%d", err, emitted, public, reader.bytes, limit)
	}
}
