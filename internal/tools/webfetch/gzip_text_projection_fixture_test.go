package webfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

type webGzipHTTPFixture struct {
	body          []byte
	contentType   string
	encoding      string
	status        int
	missingLength int
}

func encodeWebGzipFixture(t *testing.T, payload []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func fetchWebGzipFixture(t *testing.T, fixture webGzipHTTPFixture, limit int64) Result {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", fixture.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(fixture.body)+fixture.missingLength))
		if fixture.encoding != "" {
			w.Header().Set("Content-Encoding", fixture.encoding)
		}
		if fixture.status != 0 {
			w.WriteHeader(fixture.status)
		}
		_, _ = w.Write(fixture.body)
	}))
	t.Cleanup(server.Close)
	result, err := FetchWithOptions(context.Background(), server.URL+"/resource", limit, Options{
		ClientForURL: func(context.Context, string) (*http.Client, error) { return server.Client(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertWebGzipPrefixAuthority(t *testing.T, result Result, returned []byte, limit int64) []byte {
	t.Helper()
	if int64(len(returned)) > limit {
		returned = returned[:limit]
	}
	digest := sha256.Sum256(returned)
	if result.BytesRead != len(returned) || result.BodySHA256 != hex.EncodeToString(digest[:]) || result.BodyHashScope != "returned_response_bytes" {
		t.Fatalf("original received prefix changed: bytes=%d want=%d hash=%q scope=%q",
			result.BytesRead, len(returned), result.BodySHA256, result.BodyHashScope)
	}
	return returned
}
