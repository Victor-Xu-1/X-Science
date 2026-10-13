package websearch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebSearchUsesOnlyExplicitProxyAuthority(t *testing.T) {
	var ambient, explicit, direct atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		_, _ = io.WriteString(w, "source fixture")
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		explicit.Add(1)
		http.Error(w, "controlled proxy failure", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(*http.Request) (*url.URL, error) {
		ambient.Add(1)
		return url.Parse("http://127.0.0.1:9")
	}
	base := &http.Client{Transport: transport}
	client, err := newWebSearchHTTPClient(time.Second, base, "")(context.Background(), origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if ambient.Load() != 0 || direct.Load() != 1 || transport.Proxy == nil {
		t.Fatal("search inherited ambient proxy or changed its caller transport")
	}
	client, err = newWebSearchHTTPClient(time.Second, base, proxy.URL)(context.Background(), "https://source.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get("https://source.example.com/"); err == nil {
		response.Body.Close()
		t.Fatal("expected controlled explicit proxy failure")
	}
	if ambient.Load() != 0 || explicit.Load() != 1 || direct.Load() != 1 {
		t.Fatal("explicit search proxy lost authority or fell back to direct")
	}
}
