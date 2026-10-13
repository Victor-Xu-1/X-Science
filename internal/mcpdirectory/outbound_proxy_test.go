package mcpdirectory

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type forbiddenProxyLocalResolver struct{ calls atomic.Int64 }

func (r *forbiddenProxyLocalResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls.Add(1)
	return []netip.Addr{netip.MustParseAddr("198.18.0.1")}, nil
}

func TestPublicSourceExplicitProxyOwnsDNSAndPreservesRedirectPolicy(t *testing.T) {
	var originCalls, proxyCalls atomic.Int64
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://127.0.0.1/private", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "source bytes")
	}))
	defer origin.Close()
	target, _ := url.Parse(origin.URL)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "source.example.com:443" {
			http.Error(w, "wrong target", 400)
			return
		}
		upstream, err := net.DialTimeout("tcp", target.Host, time.Second)
		if err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer client.Close()
		defer upstream.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, client); upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		client.Close()
		<-done
	}))
	defer proxy.Close()
	base := origin.Client()
	transport := base.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true // Local TLS fixture only; production TLS remains unchanged.
	base = &http.Client{Transport: transport, Timeout: 3 * time.Second}
	resolver := &forbiddenProxyLocalResolver{}
	client, err := NewPublicHTTPClient(base, resolver, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("https://source.example.com/source")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "source bytes" || resolver.calls.Load() != 0 {
		t.Fatalf("body=%q local DNS calls=%d", body, resolver.calls.Load())
	}
	if _, err := client.Get("https://source.example.com/redirect"); err == nil {
		t.Fatal("private redirect accepted")
	}
	before := proxyCalls.Load()
	for _, raw := range []string{"https://localhost/", "https://10.0.0.1/", "https://198.18.0.1/", "https://metadata.internal/", "https://user:secret@source.example.com/", "http://source.example.com/"} {
		if _, err := client.Get(raw); err == nil {
			t.Errorf("unsafe target accepted: %s", raw)
		}
	}
	if proxyCalls.Load() != before || originCalls.Load() != 2 {
		t.Fatalf("unsafe target reached proxy/origin: %d %d", proxyCalls.Load(), originCalls.Load())
	}
	// Merely supplying a proxied HTTP client is not trusted DNS configuration.
	proxyURL, _ := url.Parse(proxy.URL)
	transport.Proxy = http.ProxyURL(proxyURL)
	untrusted, err := NewPublicHTTPClient(base, resolver, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Get("https://source.example.com/source"); err == nil {
		t.Fatal("ambient proxy bypassed public DNS checks")
	}
	if resolver.calls.Load() == 0 || proxyCalls.Load() != before {
		t.Fatal("untrusted proxy acquired resolver authority")
	}
}

func TestPublicSourceProxyFailureNeverFallsBackToDirect(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer proxy.Close()
	resolver := &forbiddenProxyLocalResolver{}
	client, err := NewPublicHTTPClient(&http.Client{Timeout: time.Second}, resolver, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get("https://source.example.com/"); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("proxy failure=%v", err)
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("failed proxy silently retried direct DNS")
	}
	if _, err := NewPublicHTTPClient(nil, nil, "socks5://127.0.0.1:9"); err == nil {
		t.Fatal("invalid operator proxy accepted")
	}
}
