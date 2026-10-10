package httpreliability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAttemptProgressOutlivesHeaderBudgetAndDetectsRealIdle(t *testing.T) {
	for _, idle := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			for index := 0; index < 8; index++ {
				delay := 15 * time.Millisecond
				if idle {
					delay = 300 * time.Millisecond
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(delay):
				}
				_, _ = io.WriteString(w, "x")
				w.(http.Flusher).Flush()
			}
		}))
		attempt := Begin(context.Background(), 60*time.Millisecond)
		request, _ := http.NewRequestWithContext(attempt.Context, http.MethodGet, server.URL, nil)
		response, err := server.Client().Do(request)
		if err != nil {
			attempt.Close()
			server.Close()
			t.Fatal(err)
		}
		body, err := attempt.Body(response.Body, 100*time.Millisecond)
		if err != nil {
			attempt.Close()
			server.Close()
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(body)
		_ = body.Close()
		server.Close()
		if idle && !errors.Is(readErr, ErrBodyIdleTimeout) {
			t.Fatalf("idle error=%v", readErr)
		}
		if !idle && (readErr != nil || string(value) != "xxxxxxxx") {
			t.Fatalf("progressing response=%q error=%v", value, readErr)
		}
	}
}

func TestAttemptHeaderBudgetAndParentCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		parent, cancel := context.WithCancel(context.Background())
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		attempt := Begin(parent, 40*time.Millisecond)
		if cancelParent {
			cancel()
		}
		request, _ := http.NewRequestWithContext(attempt.Context, http.MethodGet, server.URL, nil)
		_, err := server.Client().Do(request)
		err = attempt.Error(err)
		attempt.Close()
		cancel()
		server.Close()
		want := error(ErrResponseHeadersTimeout)
		if cancelParent {
			want = context.Canceled
		}
		if !errors.Is(err, want) {
			t.Fatalf("header/cancel error=%v want=%v", err, want)
		}
	}
}

func TestIdleBodyKeepsLastChunkWhenTimerRacesWithProgress(t *testing.T) {
	body := IdleBody(&delayedReadCloser{delay: 25 * time.Millisecond, data: []byte("complete")}, 10*time.Millisecond)
	value, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(value) != "complete" {
		t.Fatalf("raced progress=%q error=%v", value, err)
	}
}

func TestIdleBodyKeepsCleanEOFWhenTimerRacesWithCompletion(t *testing.T) {
	body := IdleBody(&delayedReadCloser{delay: 25 * time.Millisecond}, 10*time.Millisecond)
	value, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || len(value) != 0 {
		t.Fatalf("clean EOF raced with idle timer: %q error=%v", value, err)
	}
}

func TestIdleBodyStillTimesOutWhenNoBytesArrive(t *testing.T) {
	body := IdleBody(&hangUntilClose{closed: make(chan struct{})}, 15*time.Millisecond)
	_, err := io.ReadAll(body)
	_ = body.Close()
	if !errors.Is(err, ErrBodyIdleTimeout) {
		t.Fatalf("idle error=%v", err)
	}
}

type delayedReadCloser struct {
	delay time.Duration
	data  []byte
	slept bool
}

func (body *delayedReadCloser) Read(target []byte) (int, error) {
	if !body.slept {
		time.Sleep(body.delay)
		body.slept = true
	}
	if len(body.data) == 0 {
		return 0, io.EOF
	}
	count := copy(target, body.data)
	body.data = body.data[count:]
	if len(body.data) == 0 {
		return count, io.EOF
	}
	return count, nil
}

func (body *delayedReadCloser) Close() error { return nil }

type hangUntilClose struct {
	closed chan struct{}
}

func (body *hangUntilClose) Read([]byte) (int, error) {
	<-body.closed
	return 0, io.ErrClosedPipe
}

func (body *hangUntilClose) Close() error {
	select {
	case <-body.closed:
	default:
		close(body.closed)
	}
	return nil
}

func TestRetryAdviceNeverRetriesEarlyAndRemainsCancelable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if RetryAfter("120", now) != 2*time.Minute || RetryAfter(now.Add(2*time.Minute).Format(http.TimeFormat), now) != 2*time.Minute {
		t.Fatal("server retry advice shortened")
	}
	for _, invalid := range []string{"-2", "invalid", "\r\n"} {
		if RetryAfter(invalid, now) != 0 {
			t.Fatal("invalid retry advice accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("backoff ignored cancel: %v", err)
	}
	if !TransientStatus(429) || !TransientStatus(503) || TransientStatus(404) || TransientStatus(501) {
		t.Fatal("HTTP retry classification")
	}
}
