package providers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestBoundedProviderBodySizeWinsOverReadError(t *testing.T) {
	_, err := readBoundedProviderBody(&sizedBodyErrorReader{remaining: 17}, 16)
	if !errors.Is(err, errProviderResponseTooLarge) {
		t.Fatalf("oversized response lost permanent cause: %v", err)
	}
}

type sizedBodyErrorReader struct{ remaining int }

func (r *sizedBodyErrorReader) Read(target []byte) (int, error) {
	count := min(len(target), r.remaining)
	clear(target[:count])
	r.remaining -= count
	if r.remaining == 0 {
		return count, io.ErrUnexpectedEOF
	}
	return count, nil
}

func TestRuntimeModelDroppedThrottleBodyKeepsRetryAfter(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("retry later"))
			}))
			defer server.Close()
			adapter, err := defaultProviderAdapters.resolve(ProtocolOpenAICompatible)
			if err != nil {
				t.Fatal(err)
			}
			client := &runtimeModelClient{profile: ModelProfile{Model: "policy"}, adapter: adapter,
				httpClient: &http.Client{Transport: &firstBodyResetTransport{next: server.Client().Transport}}}
			_, retryable, err := client.completeJSON(t.Context(), 1, server.URL, []byte("{}"), agentruntime.ModelRequest{}, nil)
			if !retryable || providerRetryDelay(1, err) != 5*time.Second {
				t.Fatalf("status=%d retryable=%t delay=%v error=%v", status, retryable, providerRetryDelay(1, err), err)
			}
		})
	}
}

func TestJSONAdaptersRecoverRealTruncatedSuccessBody(t *testing.T) {
	for _, adapter := range []string{"simple", "runtime"} {
		t.Run(adapter, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) == 1 {
					w.Header().Set("Content-Length", "128")
					_, _ = w.Write([]byte("{\"choices\":["))
					return
				}
				_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"recovered\"}}]}"))
			}))
			defer server.Close()
			var client agentruntime.ModelClient = agentruntime.OpenAIChatClient{
				Endpoint: server.URL, Model: "policy", MaxAttempts: 2, HTTPClient: server.Client()}
			if adapter == "runtime" {
				var err error
				client, err = NewRuntimeModelClient(ModelProfile{
					Provider: ProviderProfile{ID: "policy", Type: "openai", Protocol: ProtocolOpenAICompatible, Endpoint: server.URL},
					Model:    "policy", Request: RequestProfile{MaxAttempts: 2}}, server.Client(), nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			response, err := client.Complete(t.Context(), agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "read"}}})
			if err != nil || response.Message.Content != "recovered" || calls.Load() != 2 {
				t.Fatalf("response=%#v calls=%d error=%v", response, calls.Load(), err)
			}
		})
	}
}
