package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func usageObjectSnapshotFrame(t *testing.T, protocol string, receipt usageObjectReceipt) string {
	t.Helper()
	name, key := "message", "usage"
	event := map[string]any{}
	switch protocol {
	case ProtocolOpenAICompatible:
		event["id"], event["choices"] = "usage-object", []any{}
	case ProtocolOpenAIResponses:
		name = "response.created"
		event["type"] = name
		// created is not a usage-receipt adoption point. Do not add one.
		response := map[string]any{}
		if receipt.present {
			response[key] = receipt.value
		}
		event["response"] = response
	case ProtocolAnthropic:
		name = "message_delta"
		event["type"] = name
	case ProtocolGemini:
		key = "usageMetadata"
	}
	if receipt.present && protocol != ProtocolOpenAIResponses {
		event[key] = receipt.value
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", name, raw)
}

func appendUsageObjectSnapshot(t *testing.T, protocol, payload string, receipt usageObjectReceipt) string {
	t.Helper()
	frame := usageObjectSnapshotFrame(t, protocol, receipt)
	switch protocol {
	case ProtocolOpenAICompatible:
		return strings.Replace(payload, "data: [DONE]", frame+"data: [DONE]", 1)
	case ProtocolAnthropic:
		return strings.Replace(payload, "event: message_stop", frame+"event: message_stop", 1)
	default:
		return payload + frame
	}
}

func readUsageObjectStream(protocol, payload string, limit int64, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, providerTokenUsage, *generationProgressReader, error) {
	if emit == nil {
		emit = func(agentruntime.ModelStreamEvent) error { return nil }
	}
	reader := &generationProgressReader{Reader: strings.NewReader(payload)}
	client := &runtimeModelClient{profile: ModelProfile{Request: RequestProfile{MaxResponseBytes: limit}}}
	var response agentruntime.ModelResponse
	var usage providerTokenUsage
	var err error
	switch protocol {
	case ProtocolOpenAICompatible:
		response, _, usage, _, err = readOpenAIChatStream(reader, limit, emit)
	case ProtocolOpenAIResponses:
		response, _, usage, _, err = readOpenAIResponsesStream(reader, limit, emit, client.openAIResponsesTypedResponse)
	case ProtocolAnthropic:
		response, _, usage, _, err = readAnthropicStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
	case ProtocolGemini:
		response, _, usage, _, err = readGeminiStream(client, http.Response{Body: &generationAdmissionBody{reader}}, emit)
	}
	return response, usage, reader, err
}

func TestUsageObjectStreamSnapshotsSeparateValidityFromWarningHistory(t *testing.T) {
	// Responses accepts its receipt only at the unique completed/incomplete
	// response. The other three protocols already merge cumulative snapshots.
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolAnthropic, ProtocolGemini} {
		for _, invalidLast := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invalidLast=%t", protocol, invalidLast), func(t *testing.T) {
				bad := usageObjectReceipt{present: true, value: []any{"private-usage-junk"}}
				valid := usageObjectReceipt{present: true, value: usagePresenceWire(protocol, "complete", 0, 0, 0, 0)}
				first, last := bad, valid
				if invalidLast {
					first, last = valid, bad
				}
				payload := usageObjectStream(t, protocol, first, false)
				payload = appendUsageObjectSnapshot(t, protocol, payload, last)
				payload = appendUsageObjectSnapshot(t, protocol, payload, usageObjectReceipt{})
				payload = appendUsageObjectSnapshot(t, protocol, payload, usageObjectReceipt{present: true})
				response, usage, _, err := readUsageObjectStream(protocol, payload, 64<<10, nil)
				if err != nil || response.Message.Content != "done" || len(response.Message.ToolCalls) != 1 || usage.Warnings.Empty() {
					t.Fatalf("snapshot warning changed primary completion: usage=%+v err=%v", usage, err)
				}
				wantInvalid, wantReported := agentruntime.ModelUsageFields{}, usageObjectFields(protocol)
				if invalidLast {
					wantInvalid, wantReported = wantReported, wantInvalid
				}
				if usage.InvalidFields != wantInvalid || usage.Reported != wantReported || usage.TotalTokens != 0 {
					t.Fatalf("history poisoned healed state or unknown healed invalid state: %+v", usage)
				}
			})
		}
	}
}

func TestUsageObjectOnlyFramesDoNotRecordGrowthOrBypassBounds(t *testing.T) {
	for _, protocol := range []string{ProtocolOpenAICompatible, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			valid := usageObjectReceipt{present: true, value: usagePresenceWire(protocol, "complete", 20, 3, 23, 5)}
			plain := usageObjectStream(t, protocol, valid, false)
			payload := plain
			junk := usageObjectReceipt{present: true, value: []any{"private-usage-junk"}}
			if protocol == ProtocolOpenAIResponses {
				payload = strings.Repeat(usageObjectSnapshotFrame(t, protocol, junk), 8) + payload
			} else {
				for index := 0; index < 8; index++ {
					payload = appendUsageObjectSnapshot(t, protocol, payload, junk)
				}
			}
			_, _, baseline, err := readUsageObjectStream(protocol, plain, 64<<10, nil)
			if err != nil {
				t.Fatalf("baseline fixture failed: %v", err)
			}
			response, _, reader, err := readUsageObjectStream(protocol, payload, 64<<10, nil)
			if err != nil || response.Message.Content != "done" || len(response.Message.ToolCalls) != 1 ||
				reader.bytes != baseline.bytes || reader.observations != baseline.observations {
				t.Fatalf("usage junk became decoder growth: err=%v bytes=%d/%d observations=%d/%d", err, reader.bytes, baseline.bytes, reader.observations, baseline.observations)
			}
			_, _, _, err = readUsageObjectStream(protocol, payload, 64<<10, func(agentruntime.ModelStreamEvent) error { return context.Canceled })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("optional usage swallowed cancellation: %v", err)
			}
			oversized := usageObjectSnapshotFrame(t, protocol, usageObjectReceipt{present: true, value: strings.Repeat("x", 5000)})
			_, _, reader, err = readUsageObjectStream(protocol, oversized, 256, nil)
			if !errors.Is(err, errProviderResponseTooLarge) || reader.bytes != 0 || reader.observations != 0 {
				t.Fatalf("usage bypassed wire bounds or recorded rejected progress: %v", err)
			}
		})
	}
}
