package agentruntime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIChatOversizedDroppedBodyIsPermanent(t *testing.T) {
	client := OpenAIChatClient{Endpoint: "http://body.invalid", Model: "policy",
		HTTPClient: &http.Client{Transport: oversizedBodyTransport{}}}
	_, retryable, err := client.completeAttempt(t.Context(), []byte("{}"), nil)
	if err == nil || retryable {
		t.Fatalf("oversized body retried: retryable=%t error=%v", retryable, err)
	}
}

type oversizedBodyTransport struct{}

func (oversizedBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
		Body: io.NopCloser(&sizedBodyErrorReader{remaining: maxOpenAIChatResponseBytes + 1})}, nil
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

func TestOpenAIChatDroppedPermanentBodyDoesNotRetry(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("rejected"))
			}))
			defer server.Close()
			client := OpenAIChatClient{Endpoint: server.URL, Model: "policy",
				HTTPClient: &http.Client{Transport: &firstBodyResetTransport{next: server.Client().Transport}}}
			_, retryable, err := client.completeAttempt(t.Context(), []byte("{}"), nil)
			if err == nil || retryable {
				t.Fatalf("status=%d retryable=%t error=%v", status, retryable, err)
			}
		})
	}
}

func TestOpenAIChatDroppedThrottleBodyKeepsRetryAfter(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("retry later"))
			}))
			defer server.Close()
			client := OpenAIChatClient{Endpoint: server.URL, Model: "policy",
				HTTPClient: &http.Client{Transport: &firstBodyResetTransport{next: server.Client().Transport}}}
			_, retryable, err := client.completeAttempt(t.Context(), []byte("{}"), nil)
			if !retryable || modelRetryDelay(1, err) != 5*time.Second {
				t.Fatalf("status=%d retryable=%t delay=%v error=%v", status, retryable, modelRetryDelay(1, err), err)
			}
		})
	}
}
