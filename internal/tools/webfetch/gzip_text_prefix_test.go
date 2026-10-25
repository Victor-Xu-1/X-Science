package webfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchGzipTextPrefixIsReadableWithinDecodedLimit(t *testing.T) {
	const limit = 1800
	var value strings.Builder
	for number := 0; number < 256; number++ {
		digest := sha256.Sum256([]byte(strconv.Itoa(number)))
		_, _ = fmt.Fprintf(&value, "%x", digest)
	}
	payload := []byte(`{"kind":"synthetic","value":"` + value.String() + `"}`)
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := encoded.Bytes()
	if len(compressed) <= limit || len(payload) <= limit {
		t.Fatal("fixture must exceed both the received-byte and decoded-text caps")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/resource.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(compressed)))
		// Deliberately omit Content-Encoding. The exact observed defect is a
		// recognizable gzip text representation reaching the charset boundary.
		_, _ = w.Write(compressed)
	}))
	t.Cleanup(server.Close)
	result, err := FetchWithOptions(context.Background(), server.URL+"/resource.json", limit, Options{
		ClientForURL: func(context.Context, string) (*http.Client, error) { return server.Client(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceUnavailable || result.Error != "" || result.Binary || result.Body != string(payload[:limit]) {
		t.Fatalf("bounded gzip text is not readable: unavailable=%t binary=%t error=%q text_bytes=%d",
			result.SourceUnavailable, result.Binary, result.Error, len(result.Body))
	}
	if len(result.Body) > limit || !result.Partial || !result.Truncated || result.Complete || result.Warning == "" {
		t.Fatalf("decoded prefix was promoted to complete: partial=%t truncated=%t complete=%t text_bytes=%d warning=%q",
			result.Partial, result.Truncated, result.Complete, len(result.Body), result.Warning)
	}
	wantRaw := compressed[:limit]
	wantDigest := sha256.Sum256(wantRaw)
	if result.BytesRead != limit || result.BodySHA256 != hex.EncodeToString(wantDigest[:]) || result.BodyHashScope != "returned_response_bytes" {
		t.Fatalf("original byte accounting changed: bytes=%d sha=%q scope=%q", result.BytesRead, result.BodySHA256, result.BodyHashScope)
	}
	restored, err := base64.StdEncoding.DecodeString(result.RawBodyBase64)
	if err != nil || !bytes.Equal(restored, wantRaw) {
		t.Fatalf("original gzip prefix is not recoverable: restored_bytes=%d error=%v", len(restored), err)
	}
	if requests.Load() != 1 {
		t.Fatalf("codec projection re-requested the source: requests=%d", requests.Load())
	}
}
