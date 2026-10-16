package providers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestProviderAdmittedGrowthPrecedesExternalCallbacks(t *testing.T) {
	for _, fixture := range []struct {
		name, data string
		read       func(*runtimeModelClient, *generationProgressReader, func(agentruntime.ModelStreamEvent) error) error
	}{
		{"chat/content", `{"choices":[{"delta":{"content":"fixture"}}]}`, readCallbackOrderChat},
		{"chat/private", `{"choices":[{"delta":{"reasoning_content":"fixture"}}]}`, readCallbackOrderChat},
		{"chat/tool", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`, readCallbackOrderChat},
		{"responses/content", `{"type":"response.output_text.delta","delta":"fixture"}`, readCallbackOrderResponses},
		{"responses/private", `{"type":"response.reasoning_summary_text.delta","delta":"fixture"}`, readCallbackOrderResponses},
		{"responses/tool", `{"type":"response.output_item.added","item":{"type":"function_call","id":"item","call_id":"call","name":"lookup"}}`, readCallbackOrderResponses},
		{"anthropic/content", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fixture"}}`, readCallbackOrderAnthropic},
		{"anthropic/private", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"fixture"}}`, readCallbackOrderAnthropic},
		{"anthropic/tool", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call","name":"lookup"}}`, readCallbackOrderAnthropic},
		{"gemini/content", `{"candidates":[{"content":{"parts":[{"text":"fixture"}]}}]}`, readCallbackOrderGemini},
		{"gemini/private", `{"candidates":[{"content":{"parts":[{"text":"fixture","thought":true}]}}]}`, readCallbackOrderGemini},
		{"gemini/tool", `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]}}]}`, readCallbackOrderGemini},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: 4096}}}
			callbackErr := errors.New("callback cancellation fixture")
			callbacks := 0
			err := fixture.read(client, reader, func(agentruntime.ModelStreamEvent) error {
				callbacks++
				if reader.observations != 1 || reader.bytes <= 0 {
					t.Errorf("callback preceded decoder growth: observations=%d bytes=%d", reader.observations, reader.bytes)
				}
				return callbackErr
			})
			if callbacks != 1 || !errors.Is(err, callbackErr) {
				t.Fatalf("callback cancellation lost precedence: callbacks=%d err=%v", callbacks, err)
			}
		})
	}
}

func readCallbackOrderChat(_ *runtimeModelClient, reader *generationProgressReader, emit func(agentruntime.ModelStreamEvent) error) error {
	_, _, _, _, err := readOpenAIChatStream(reader, 4096, emit)
	return err
}

func TestProviderInvalidStreamShapeCannotPublishCallbackOrGrowth(t *testing.T) {
	for _, fixture := range []struct {
		name, data string
		read       func(*runtimeModelClient, *generationProgressReader, func(agentruntime.ModelStreamEvent) error) error
	}{
		{"chat/missing_index", `{"choices":[{"delta":{"content":"fixture","tool_calls":[{"id":"call","function":{"name":"lookup"}}]}}]}`, readCallbackOrderChat},
		{"chat/negative_index", `{"choices":[{"delta":{"content":"fixture","tool_calls":[{"index":-1,"id":"call","function":{"name":"lookup"}}]}}]}`, readCallbackOrderChat},
		{"chat/unsupported_type", `{"choices":[{"delta":{"content":"fixture","tool_calls":[{"index":0,"id":"call","type":"invalid","function":{"name":"lookup"}}]}}]}`, readCallbackOrderChat},
		{"chat/changed_id", `{"choices":[{"delta":{"content":"fixture","tool_calls":[{"index":0,"id":"call","function":{"name":"lookup"}},{"index":0,"id":"changed","function":{"arguments":"{}"}}]}}]}`, readCallbackOrderChat},
		{"chat/duplicate_choice", `{"choices":[{"index":0,"delta":{"content":"fixture"}},{"index":0,"delta":{}}]}`, readCallbackOrderChat},
		{"gemini/empty_name_with_text", `{"candidates":[{"content":{"parts":[{"text":"fixture","functionCall":{"name":" ","args":{}}}]}}]}`, readCallbackOrderGemini},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: 4096}}}
			callbacks := 0
			err := fixture.read(client, reader, func(agentruntime.ModelStreamEvent) error {
				callbacks++
				return nil
			})
			if err == nil || callbacks != 0 || reader.observations != 0 || reader.bytes != 0 {
				t.Fatalf("invalid shape reached callback/growth: err=%v callbacks=%d observations=%d bytes=%d", err, callbacks, reader.observations, reader.bytes)
			}
		})
	}
}

func readCallbackOrderResponses(client *runtimeModelClient, reader *generationProgressReader, emit func(agentruntime.ModelStreamEvent) error) error {
	_, _, _, _, err := readOpenAIResponsesStream(reader, 4096, emit, client.openAIResponsesTypedResponse)
	return err
}

func readCallbackOrderAnthropic(client *runtimeModelClient, reader *generationProgressReader, emit func(agentruntime.ModelStreamEvent) error) error {
	_, _, _, _, err := readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
	return err
}

func readCallbackOrderGemini(client *runtimeModelClient, reader *generationProgressReader, emit func(agentruntime.ModelStreamEvent) error) error {
	_, _, _, _, err := readGeminiStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
	return err
}
