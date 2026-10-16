package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

const responsesIdentityGrowthFixture = `{"type":"response.output_item.added","item":{"type":"function_call","id":"item","call_id":"call","name":"lookup"}}`

func TestProviderResponsesIdentityGrowthIsUniqueAndBounded(t *testing.T) {
	for _, fixture := range []struct {
		name, data   string
		observations int
	}{
		{"first", responsesIdentityGrowthFixture, 1},
		{"duplicate", responsesIdentityGrowthFixture + "\n\ndata: " + responsesIdentityGrowthFixture, 1},
		{"conflict", responsesIdentityGrowthFixture + "\n\ndata: " + strings.Replace(responsesIdentityGrowthFixture, `"call_id":"call"`, `"call_id":"changed"`, 1), 1},
		{"ignored", strings.Replace(responsesIdentityGrowthFixture, `"type":"function_call"`, `"type":"unknown_fixture"`, 1), 0},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := &generationProgressReader{Reader: strings.NewReader("data: " + fixture.data + "\n\n")}
			client := &runtimeModelClient{}
			response, _, _, _, err := readOpenAIResponsesStream(reader, 4096, nil, client.openAIResponsesTypedResponse)
			wantBytes := int64(0)
			if fixture.observations > 0 {
				wantBytes = int64(len("item") + len("call") + len("lookup"))
			}
			if err == nil || reader.observations != fixture.observations || reader.bytes != wantBytes || len(response.Message.ToolCalls) != 0 {
				t.Fatalf("identity growth/execution changed: err=%v observations=%d bytes=%d calls=%v", err, reader.observations, reader.bytes, response.Message.ToolCalls)
			}
		})
	}
	var stream strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&stream, "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_%03d\",\"call_id\":\"call_%03d\",\"name\":\"lookup\"}}\n\n", i, i)
	}
	reader := &generationProgressReader{Reader: strings.NewReader(stream.String())}
	response, _, _, _, err := readOpenAIResponsesStream(reader, 512, nil, (&runtimeModelClient{}).openAIResponsesTypedResponse)
	if !errors.Is(err, errProviderResponseTooLarge) || reader.bytes <= 0 || reader.bytes > 512 || len(response.Message.ToolCalls) != 0 {
		t.Fatalf("identity metadata escaped semantic cap: err=%v growth=%d calls=%v", err, reader.bytes, response.Message.ToolCalls)
	}
}

func TestProviderResponsesIdentityHandsOffInitialDeadline(t *testing.T) {
	const window = 240 * time.Millisecond
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for _, step := range []struct {
			delay time.Duration
			data  string
		}{
			{window / 2, responsesIdentityGrowthFixture},
			{window * 2 / 3, `{"type":"response.function_call_arguments.delta","item_id":"item","delta":"{}"}` + "\n\ndata: " +
				`{"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"function_call","id":"item","call_id":"call","name":"lookup","arguments":"{}"}]}}`},
		} {
			timer := time.NewTimer(step.delay)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			fmt.Fprintf(w, "data: %s\n\n", step.data)
			w.(http.Flusher).Flush()
		}
	}))
	defer api.Close()
	client, err := NewRuntimeModelClient(ModelProfile{
		Provider: ProviderProfile{ID: "identity-growth-fixture", Protocol: ProtocolOpenAIResponses, Endpoint: api.URL},
		Model:    "fixture", Request: RequestProfile{Timeout: window, MaxAttempts: 1, MaxResponseBytes: 4096},
	}, api.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	boundaries := 0
	response, err := client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
		Tools: []agentruntime.ToolSchema{{Name: "lookup", Parameters: map[string]any{"type": "object"}}},
	}, func(event agentruntime.ModelStreamEvent) error {
		if event.Kind == agentruntime.ModelStreamEventToolCallBoundary {
			boundaries++
		}
		return nil
	})
	if err != nil || ctx.Err() != nil || boundaries != 1 || len(response.Message.ToolCalls) != 1 || string(response.Message.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("admitted identity did not hand off initial deadline: err=%v parent=%v boundaries=%d calls=%v", err, ctx.Err(), boundaries, response.Message.ToolCalls)
	}
}
