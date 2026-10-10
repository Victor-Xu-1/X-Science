package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIChatClientRetriesTransientFailureWithBackoff(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client := OpenAIChatClient{Endpoint: server.URL, Model: "retry-model", HTTPClient: server.Client(), MaxAttempts: 2}
	started := time.Now()
	response, err := client.Complete(t.Context(), ModelRequest{Messages: []Message{{Role: "user", Content: "continue"}}})
	if err != nil || response.Message.Content != "ok" {
		t.Fatalf("response=%#v error=%v", response, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if elapsed := time.Since(started); elapsed < modelRetryBaseDelay/2 {
		t.Fatalf("retry happened without backoff: %s", elapsed)
	}
}

func TestParseModelRetryAfterCapsAndRejectsInvalidValues(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	futureDate := func(delta time.Duration) string {
		return now.Add(delta).UTC().Format(http.TimeFormat)
	}
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"empty", "", 0},
		{"whitespace", "   ", 0},
		{"seconds", "29", 29 * time.Second},
		{"seconds at cap boundary", "30", modelRetryAfterCap},
		{"seconds above cap", "120", modelRetryAfterCap},
		{"zero seconds", "0", 0},
		{"negative seconds", "-5", 0},
		{"garbage", "soon", 0},
		{"http date slightly ahead", futureDate(10 * time.Second), 10 * time.Second},
		{"http date far ahead is capped", futureDate(2 * time.Hour), modelRetryAfterCap},
		{"http date in the past", futureDate(-time.Minute), 0},
		{"invalid http date", "Oct 10 12:00:00 2026", 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := parseModelRetryAfter(testCase.value, now); got != testCase.want {
				t.Fatalf("parseModelRetryAfter(%q) = %s, want %s", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestModelRetryDelayHonorsRetryAfterWithoutJitter(t *testing.T) {
	retryErr := &modelRetryError{err: errors.New("rate limited"), retryAfter: 7 * time.Second}
	for _, attempt := range []int{1, 2, 5, 50} {
		if got := modelRetryDelay(attempt, retryErr); got != 7*time.Second {
			t.Fatalf("modelRetryDelay(attempt=%d) with Retry-After = %s, want exactly 7s", attempt, got)
		}
	}

	// A wrapped retry error keeps its Retry-After through the error chain.
	wrapped := fmt.Errorf("model request failed: %w", retryErr)
	if got := modelRetryDelay(3, wrapped); got != 7*time.Second {
		t.Fatalf("modelRetryDelay with wrapped Retry-After = %s, want exactly 7s", got)
	}

	// A zero Retry-After must not stall the retry loop; it falls back to backoff.
	fallback := &modelRetryError{err: errors.New("transient"), retryAfter: 0}
	if got := modelRetryDelay(1, fallback); got < modelRetryBaseDelay/2 || got > modelRetryBaseDelay {
		t.Fatalf("zero Retry-After delay = %s, want backoff in [%s, %s]", got, modelRetryBaseDelay/2, modelRetryBaseDelay)
	}
}

func TestModelRetryDelayBackoffStaysWithinJitterBounds(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		low     time.Duration
		high    time.Duration
	}{
		{"first attempt", 1, modelRetryBaseDelay / 2, modelRetryBaseDelay},
		{"second attempt", 2, modelRetryBaseDelay, 2 * modelRetryBaseDelay},
		{"third attempt", 3, 2 * modelRetryBaseDelay, 4 * modelRetryBaseDelay},
		{"capped attempt", 4, modelRetryMaxDelay / 2, modelRetryMaxDelay},
		{"far past cap", 20, modelRetryMaxDelay / 2, modelRetryMaxDelay},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for range 64 {
				got := modelRetryDelay(testCase.attempt, nil)
				if got < testCase.low || got > testCase.high {
					t.Fatalf("modelRetryDelay(attempt=%d) = %s, want in [%s, %s]", testCase.attempt, got, testCase.low, testCase.high)
				}
			}
		})
	}
}

func TestModelRetryDelayTreatsNonPositiveAttemptsAsFirst(t *testing.T) {
	for _, attempt := range []int{-3, 0} {
		got := modelRetryDelay(attempt, nil)
		if got < modelRetryBaseDelay/2 || got > modelRetryBaseDelay {
			t.Fatalf("modelRetryDelay(attempt=%d) = %s, want first-attempt range [%s, %s]", attempt, got, modelRetryBaseDelay/2, modelRetryBaseDelay)
		}
	}
}

func TestModelRetryTimingMatchesV11Policy(t *testing.T) {
	if modelRetryBaseDelay != 800*time.Millisecond {
		t.Fatalf("modelRetryBaseDelay = %s, want 800ms", modelRetryBaseDelay)
	}
	if modelRetryMaxDelay != 6*time.Second {
		t.Fatalf("modelRetryMaxDelay = %s, want 6s", modelRetryMaxDelay)
	}
}

func TestWaitModelRetryHonorsCancellationWithoutSleepingRetryAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := waitModelRetry(ctx, 1, &modelRetryError{err: errors.New("rate limited"), retryAfter: 30 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitModelRetry error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("waitModelRetry blocked on Retry-After after cancellation: %s", elapsed)
	}
}

func TestOpenAIChatClientRetryWaitHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	firstCall := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		select {
		case firstCall <- struct{}{}:
		default:
		}
		w.Header().Set("Retry-After", "30")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := OpenAIChatClient{Endpoint: server.URL, Model: "retry-model", HTTPClient: server.Client(), MaxAttempts: 3}
	completed := make(chan error, 1)
	go func() {
		_, err := client.Complete(ctx, ModelRequest{Messages: []Message{{Role: "user", Content: "continue"}}})
		completed <- err
	}()
	select {
	case <-firstCall:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("first model request did not reach the test server")
	}
	var err error
	select {
	case err = <-completed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not interrupt retry wait")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d; cancellation must stop replay", calls.Load())
	}
}
