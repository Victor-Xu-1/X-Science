package webfetch

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFetchGzipTextExpansionStaysBoundedAndTruthful(t *testing.T) {
	small := []byte(`{"value":"synthetic"}`)
	smallGzip := encodeWebGzipFixture(t, small)
	doubleGzip := encodeWebGzipFixture(t, smallGzip)
	large := []byte(`{"value":"` + strings.Repeat("x", 8192) + `"}`)
	exact := []byte(`{"v":"` + strings.Repeat("x", 56) + `"}`)
	for _, test := range []struct {
		name         string
		fixture      webGzipHTTPFixture
		limit        int64
		returned     []byte
		wantText     string
		layers       int
		truncated    bool
		uncompressed bool
	}{
		{name: "small-complete", fixture: webGzipHTTPFixture{body: smallGzip, contentType: "application/json"},
			limit: 128, returned: smallGzip, wantText: string(small), layers: 1},
		{name: "tiny-input-large-expansion", fixture: webGzipHTTPFixture{body: encodeWebGzipFixture(t, large), contentType: "application/json"},
			limit: 256, returned: encodeWebGzipFixture(t, large), wantText: string(large[:256]), layers: 1, truncated: true},
		{name: "exact-decoded-cap", fixture: webGzipHTTPFixture{body: encodeWebGzipFixture(t, exact), contentType: "application/json"},
			limit: 64, returned: encodeWebGzipFixture(t, exact), wantText: string(exact), layers: 1},
		{name: "utf8-boundary", fixture: webGzipHTTPFixture{body: encodeWebGzipFixture(t, []byte(strings.Repeat("🧬", 64))), contentType: "text/plain; charset=utf-8"},
			limit: 65, returned: encodeWebGzipFixture(t, []byte(strings.Repeat("🧬", 64))), wantText: strings.Repeat("🧬", 16), layers: 1, truncated: true},
		{name: "charset-expansion-cap", fixture: webGzipHTTPFixture{body: encodeWebGzipFixture(t, bytes.Repeat([]byte{0xe9}, 128)), contentType: "text/plain; charset=iso-8859-1"},
			limit: 65, returned: encodeWebGzipFixture(t, bytes.Repeat([]byte{0xe9}, 128)), wantText: strings.Repeat("é", 32), layers: 1, truncated: true},
		{name: "transparent-gzip", fixture: webGzipHTTPFixture{body: smallGzip, contentType: "application/json", encoding: "gzip"},
			limit: 128, returned: small, wantText: string(small), uncompressed: true},
		{name: "transparent-outer-residual-inner", fixture: webGzipHTTPFixture{body: doubleGzip, contentType: "application/json", encoding: "gzip"},
			limit: 128, returned: smallGzip, wantText: string(small), layers: 1, uncompressed: true},
		{name: "unannounced-double-gzip", fixture: webGzipHTTPFixture{body: doubleGzip, contentType: "application/json"},
			limit: 128, returned: doubleGzip, wantText: string(small), layers: 2},
		{name: "plain-text", fixture: webGzipHTTPFixture{body: small, contentType: "application/json"},
			limit: 128, returned: small, wantText: string(small)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := fetchWebGzipFixture(t, test.fixture, test.limit)
			if result.SourceUnavailable || result.Error != "" || result.Binary || result.Body != test.wantText || !utf8.ValidString(result.Body) || int64(len(result.Body)) > test.limit {
				t.Fatalf("text projection changed or exceeded its cap: result=%#v", result)
			}
			if result.Truncated != test.truncated || result.Partial != test.truncated || result.Complete == test.truncated {
				t.Fatalf("decoded prefix completeness is false: partial=%t truncated=%t complete=%t", result.Partial, result.Truncated, result.Complete)
			}
			raw := assertWebGzipPrefixAuthority(t, result, test.returned, test.limit)
			if result.Uncompressed != test.uncompressed {
				t.Fatalf("transparent transport decoding was misreported: got=%t want=%t", result.Uncompressed, test.uncompressed)
			}
			if test.layers == 0 {
				if result.TextDecoding != nil || result.RawBodyBase64 != "" {
					t.Fatal("plain or transparently decoded text was decoded again")
				}
				return
			}
			if result.TextDecoding == nil || result.TextDecoding.Format != "gzip" || result.TextDecoding.Layers != test.layers || result.TextDecoding.DecodedBytes != len(test.wantText) || result.TextDecoding.Truncated != test.truncated {
				t.Fatalf("content projection receipt is not truthful: %#v", result.TextDecoding)
			}
			restored, err := base64.StdEncoding.DecodeString(result.RawBodyBase64)
			if err != nil || !bytes.Equal(restored, raw) {
				t.Fatal("content projection lost the original source bytes")
			}
		})
	}
}

func TestFetchGzipTextFailurePreservesOriginalBytes(t *testing.T) {
	small := []byte(`{"value":"synthetic"}`)
	valid := encodeWebGzipFixture(t, small)
	badCRC := append([]byte(nil), valid...)
	badCRC[len(badCRC)-8] ^= 0xff
	invalidGzip := []byte{0x1f, 0x8b, 0x08, 0, 0, 0, 0, 0, 0, 0, 0x07}
	for _, test := range []struct {
		name         string
		fixture      webGzipHTTPFixture
		wantError    string
		wantText     string
		binary       bool
		wantPartial  bool
		wantComplete bool
	}{
		{name: "invalid-gzip", fixture: webGzipHTTPFixture{body: invalidGzip, contentType: "application/json"}, wantError: "content compression", wantPartial: true, wantComplete: true},
		{name: "bad-crc", fixture: webGzipHTTPFixture{body: badCRC, contentType: "application/json"}, wantError: "content compression", wantPartial: true, wantComplete: true},
		{name: "decoded-invalid-utf8", fixture: webGzipHTTPFixture{body: encodeWebGzipFixture(t, []byte{'x', 0xff, 'y'}), contentType: "text/plain; charset=utf-8"}, wantError: "character encoding", wantPartial: true, wantComplete: true},
		{name: "http-error-kept", fixture: webGzipHTTPFixture{body: valid, contentType: "application/json", status: http.StatusForbidden}, wantError: "HTTP source returned 403 Forbidden.", wantText: string(small), wantPartial: false, wantComplete: true},
		{name: "http-error-before-codec-error", fixture: webGzipHTTPFixture{body: badCRC, contentType: "application/json", status: http.StatusForbidden}, wantError: "HTTP source returned 403 Forbidden.", wantPartial: true, wantComplete: true},
		{name: "read-error-kept", fixture: webGzipHTTPFixture{body: valid, contentType: "application/json", missingLength: 7}, wantError: "Public source response could not be read completely.", wantText: string(small), wantPartial: true, wantComplete: false},
		{name: "explicit-binary", fixture: webGzipHTTPFixture{body: valid, contentType: "application/gzip"}, binary: true, wantPartial: true, wantComplete: true},
		{name: "unknown-nontext", fixture: webGzipHTTPFixture{body: valid, contentType: "application/x-custom"}, binary: true, wantPartial: true, wantComplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const limit = 512
			result := fetchWebGzipFixture(t, test.fixture, limit)
			raw := assertWebGzipPrefixAuthority(t, result, test.fixture.body, limit)
			if result.Binary != test.binary || result.Partial != test.wantPartial || result.Complete != test.wantComplete || result.Body != test.wantText {
				t.Fatalf("failed/binary source was changed into usable text: %#v", result)
			}
			if test.binary {
				if result.SourceUnavailable || result.Error != "" || result.TextDecoding != nil || result.RawBodyBase64 != "" || result.Recovery != "use_dedicated_download_or_fulltext_tool" {
					t.Fatalf("binary download handoff was bypassed: %#v", result)
				}
				return
			}
			if !result.SourceUnavailable || !strings.Contains(result.Error, test.wantError) {
				t.Fatalf("original failure was lost: unavailable=%t complete=%t error=%q", result.SourceUnavailable, result.Complete, result.Error)
			}
			restored, err := base64.StdEncoding.DecodeString(result.RawBodyBase64)
			if err != nil || !bytes.Equal(restored, raw) {
				t.Fatal("failed content decoding lost the original gzip bytes")
			}
		})
	}
	t.Run("caller-cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		served, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var once sync.Once
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(valid[:10])
			w.(http.Flusher).Flush()
			close(served)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		var outcome Result
		var outcomeErr error
		go func() {
			defer close(done)
			outcome, outcomeErr = FetchWithOptions(ctx, server.URL+"/resource", 512, Options{
				ClientForURL: func(context.Context, string) (*http.Client, error) { return server.Client(), nil },
			})
		}()
		t.Cleanup(func() {
			cancel()
			once.Do(func() { close(release) })
			server.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("owned cancelled HTTP read did not exit")
			}
		})
		select {
		case <-served:
		case <-done:
			t.Fatalf("fetch ended before the real HTTP gate: %v", outcomeErr)
		case <-time.After(5 * time.Second):
			t.Fatal("local HTTP source did not reach its owned gate")
		}
		cancel()
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("caller cancellation did not terminate the read")
		}
		if !errors.Is(outcomeErr, context.Canceled) || outcome.SourceUnavailable {
			t.Fatalf("caller cancellation became source unavailability: result=%#v error=%v", outcome, outcomeErr)
		}
	})
}
