package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestProviderUsageInvalidSnapshotMerge(t *testing.T) {
	decode := func(raw string) providerTokenUsage {
		var wire openAIChatUsage
		if err := json.Unmarshal([]byte(raw), &wire); err != nil {
			t.Fatal(err)
		}
		return wire.tokenUsage()
	}
	usage := decode(`{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23,"prompt_tokens_details":{"cached_tokens":5}}`)
	usage.merge(decode(`{"completion_tokens":"private-fixture-value","total_tokens":true}`))
	warnings := agentruntime.ModelUsageWarnings(0).Add(agentruntime.UsageFieldOutput, agentruntime.UsageCounterType).
		Add(agentruntime.UsageFieldTotal, agentruntime.UsageCounterType)
	for _, later := range []string{`{}`, `{"completion_tokens":null,"total_tokens":null}`} {
		usage.merge(decode(later))
		usage.deriveTotal(false)
		if usage.CompletionTokens != 0 || usage.TotalTokens != 0 || usage.Reported.Output || usage.Reported.Total ||
			!usage.InvalidFields.Output || !usage.InvalidFields.Total || usage.Warnings != warnings ||
			usage.PromptTokens != 20 || !usage.Reported.Input || usage.CacheReadTokens != 5 || !usage.Reported.CacheRead {
			t.Fatalf("missing/null healed an invalid counter or erased siblings: %+v", usage)
		}
	}
	usage.merge(decode(`{"completion_tokens":0,"total_tokens":20}`))
	usage.deriveTotal(false)
	if usage.CompletionTokens != 0 || usage.TotalTokens != 20 || !usage.Reported.Output || !usage.Reported.Total ||
		usage.InvalidFields.Output || usage.InvalidFields.Total || usage.Warnings != warnings {
		t.Fatalf("valid zero failed to heal current state independently of warning history: %+v", usage)
	}
}

func TestNativeProtocolsMalformedOptionalUsagePreservesCompletion(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		for _, stream := range []bool{false, true} {
			for _, invalid := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/invalid=%t", protocol, stream, invalid), func(t *testing.T) {
					wire := usagePresenceWire(protocol, "components", 20, 0, 0, 0)
					key := "output_tokens"
					if protocol == ProtocolGemini {
						key = "candidatesTokenCount"
					}
					if invalid {
						wire[key] = "private-fixture-value"
					}
					response, audit := completeUsagePresenceFixture(t, protocol, stream, wire, nil)
					fields := response.Usage.ReportedFields()
					if response.Message.Role != "assistant" || response.Message.Content != "done" ||
						len(response.Message.ToolCalls) != 0 || response.Usage.InputTokens != 20 || !fields.Input ||
						fields.Output == invalid || fields.Total || response.Usage.OutputTokens != 0 ||
						response.Usage.InvalidFields.Output != invalid || audit.Error != "" || audit.Attempt != 1 || audit.HTTPStatus != http.StatusOK {
						t.Fatalf("primary completion or usage presence changed: response=%+v audit=%+v", response, audit)
					}
					wantTotal := 20
					if invalid {
						wantTotal = 0
					}
					wantWarnings := agentruntime.ModelUsageWarnings(0)
					if invalid {
						wantWarnings = wantWarnings.Add(agentruntime.UsageFieldOutput, agentruntime.UsageCounterType)
					}
					if response.Usage.TotalTokens != wantTotal || audit.TotalTokens != wantTotal ||
						response.Usage.Warnings != wantWarnings || audit.UsageWarnings != wantWarnings {
						t.Fatalf("response/audit warning or derived total diverged: response=%+v audit=%+v", response, audit)
					}
				})
			}
		}
		t.Run(protocol+"/primary-envelope-remains-hard", func(t *testing.T) {
			body := usagePresenceBody(protocol, nil)
			body["id"] = 17
			client := &runtimeModelClient{}
			decode := client.openAIResponsesResponseDecoder
			if protocol == ProtocolAnthropic {
				decode = client.anthropicResponseDecoder
			}
			if protocol == ProtocolGemini {
				body["responseId"] = 17
				decode = client.geminiResponseDecoder
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = decode(raw, nil)
			var typeError *json.UnmarshalTypeError
			if !errors.As(err, &typeError) {
				t.Fatalf("primary identity became a warning: %v", err)
			}
		})
	}
}

func TestUsageOnlyCounterWarningsDoNotRecordGrowth(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			wire := usagePresenceWire(protocol, "components", 20, 0, 0, 0)
			key := "output_tokens"
			if protocol == ProtocolOpenAICompatible {
				key = "completion_tokens"
			}
			if protocol == ProtocolGemini {
				key = "candidatesTokenCount"
			}
			wire[key] = "private-fixture-value"
			recorder := httptest.NewRecorder()
			writeUsagePresenceStream(t, recorder, protocol, wire, nil)
			plain := recorder.Body.String()
			if protocol == ProtocolOpenAIResponses {
				plain = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\n" + plain
			}
			usageKey, eventType := "usage", "message"
			if protocol == ProtocolAnthropic {
				eventType = "message_delta"
			}
			if protocol == ProtocolGemini {
				usageKey = "usageMetadata"
			}
			event := map[string]any{usageKey: wire}
			if protocol == ProtocolOpenAICompatible {
				event["choices"] = []any{}
			}
			if protocol == ProtocolAnthropic {
				event["type"] = eventType
			}
			if protocol == ProtocolOpenAIResponses {
				eventType = "response.created"
				event = map[string]any{"type": eventType, "response": map[string]any{"usage": wire}}
			}
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			junk := strings.Repeat(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, raw), 8)
			withJunk := junk + plain
			if protocol == ProtocolOpenAICompatible {
				withJunk = strings.Replace(plain, "data: [DONE]", junk+"data: [DONE]", 1)
			}
			if protocol == ProtocolAnthropic {
				withJunk = strings.Replace(plain, "event: message_stop", junk+"event: message_stop", 1)
			}
			read := func(payload string, limit int64, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, providerTokenUsage, *generationProgressReader, error) {
				if emit == nil {
					emit = func(agentruntime.ModelStreamEvent) error { return nil }
				}
				reader := &generationProgressReader{Reader: strings.NewReader(payload)}
				client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: limit}}}
				var result agentruntime.ModelResponse
				var usage providerTokenUsage
				var err error
				switch protocol {
				case ProtocolOpenAICompatible:
					result, _, usage, _, err = readOpenAIChatStream(reader, limit, emit)
				case ProtocolOpenAIResponses:
					result, _, usage, _, err = readOpenAIResponsesStream(reader, limit, emit, client.openAIResponsesTypedResponse)
				case ProtocolAnthropic:
					result, _, usage, _, err = readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
				case ProtocolGemini:
					result, _, usage, _, err = readGeminiStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
				}
				return result, usage, reader, err
			}
			_, _, baseline, err := read(plain, 64<<10, nil)
			if err != nil {
				t.Fatalf("plain fixture failed: %v", err)
			}
			result, usage, reader, err := read(withJunk, 64<<10, nil)
			if err != nil || result.Message.Content != "done" || usage.Warnings.Empty() || reader.bytes != baseline.bytes || reader.observations != baseline.observations {
				t.Fatalf("usage-only frames became progress: err=%v observations=%d/%d bytes=%d/%d warnings=%d", err, reader.observations, baseline.observations, reader.bytes, baseline.bytes, usage.Warnings)
			}
			_, _, _, err = read(withJunk, 64<<10, func(agentruntime.ModelStreamEvent) error { return context.Canceled })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("warning erased cancellation: %v", err)
			}
			oversized := "data: {\"usage\":{\"output_tokens\":\"" + strings.Repeat("x", 5000) + "\"}}\n\n"
			_, _, reader, err = read(oversized, 256, nil)
			if !errors.Is(err, errProviderResponseTooLarge) || reader.bytes != 0 || reader.observations != 0 {
				t.Fatalf("usage bypassed wire bound: %v", err)
			}
		})
	}
}
