package httpreliability

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdleReadCompletionPreventsQueuedClose(t *testing.T) {
	body := &trackedReadCloser{Reader: strings.NewReader("complete")}
	buffer := make([]byte, 32)
	count, err := readWithIdleDeadline(body, buffer, func(expire func()) func() bool {
		return func() bool {
			expire() // Callback queued before Stop, but executes after Read.
			return false
		}
	})
	if string(buffer[:count]) != "complete" || err != nil || body.closed {
		t.Fatalf("value=%q error=%v closed=%t", buffer[:count], err, body.closed)
	}
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

func (body *trackedReadCloser) Close() error { body.closed = true; return nil }

// Buffering decoders may return useful bytes with a close-induced read error.
// Those bytes are not proof that the response finished successfully.
func TestIdleBodyRetainsPartialBytesAndTimeoutCauseOnRealHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body := IdleBody(&bufferedHTTPBody{ReadCloser: response.Body}, 25*time.Millisecond)
	defer body.Close()
	buffer := make([]byte, 32)
	count, readErr := body.Read(buffer)
	if string(buffer[:count]) != "partial" || !errors.Is(readErr, ErrBodyIdleTimeout) {
		t.Fatalf("partial=%q timeout=%t error=%v", buffer[:count], errors.Is(readErr, ErrBodyIdleTimeout), readErr)
	}
}

type bufferedHTTPBody struct{ io.ReadCloser }

func (body *bufferedHTTPBody) Read(target []byte) (int, error) {
	return io.ReadFull(body.ReadCloser, target)
}
