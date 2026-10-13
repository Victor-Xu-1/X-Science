package mcpdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/tools/mcpstdio"
)

type proxyOnlyResolver struct{ calls atomic.Int64 }

func (r *proxyOnlyResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls.Add(1)
	return []netip.Addr{netip.MustParseAddr("198.18.0.1")}, nil
}

// Exercise the production directory transport and MCP protocol together. Local
// DNS is deliberately unusable; only the explicit CONNECT proxy can reach the
// public-named, real TLS fixture. No fixture-only MCP client escape hatch is used.
func TestRemoteMCPExplicitProxyOwnsDiscoveryExecutionAndOAuthDNS(t *testing.T) {
	var localDNSCalls, proxyCalls, originCalls, toolCalls, registrationCalls atomic.Int64
	originalResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		localDNSCalls.Add(1)
		return nil, errors.New("fixture local DNS is unavailable")
	}}
	t.Cleanup(func() { net.DefaultResolver = originalResolver })
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"catalogUuid": "4f5b9638-2dc3-4b70-bbea-9af732ba57ef",
				"connectors":  []map[string]any{{"id": "directory:proxy-service", "name": "Proxy OAuth fixture", "url": "https://example.com/oauth", "transport": "http", "authRequired": true}},
			})
			return
		case "/redirect":
			http.Redirect(w, r, "https://127.0.0.1/private", http.StatusFound)
			return
		case "/oauth":
			w.Header().Set("WWW-Authenticate", `Bearer authorization_uri="https://example.com/metadata?`+r.URL.RawQuery+`"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		case "/metadata":
			metadata := map[string]any{
				"issuer": "https://example.com", "authorization_endpoint": "https://example.com/authorize",
				"token_endpoint": "https://example.com/token", "registration_endpoint": "https://example.com/register",
				"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
			}
			if field := r.URL.Query().Get("field"); field != "" {
				metadata[field] = r.URL.Query().Get("value")
			}
			_ = json.NewEncoder(w).Encode(metadata)
			return
		case "/register":
			// Stop before starting an interactive authorization/callback lifecycle.
			registrationCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid RPC fixture request", http.StatusBadRequest)
			return
		}
		result := map[string]any{}
		switch request.Method {
		case "server/discover":
			// Exercise the production initialize fallback as well as list/call.
			result["supportedVersions"] = []string{}
		case "initialize":
			result["protocolVersion"] = "2024-11-05"
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result["tools"] = []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}
		case "tools/call":
			toolCalls.Add(1)
			result["content"] = []any{map[string]any{"type": "text", "text": "proxy execution complete"}}
		default:
			http.Error(w, "unexpected RPC method", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer origin.Close()
	originURL, _ := url.Parse(origin.URL)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Method != http.MethodConnect || r.Host != "example.com:443" {
			http.Error(w, "unexpected proxy target", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", originURL.Host, time.Second)
		if err != nil {
			http.Error(w, "fixture upstream unavailable", http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		defer client.Close()
		defer upstream.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}))
	defer proxy.Close()
	base := origin.Client()
	base.Timeout = 3 * time.Second
	base.Transport.(*http.Transport).DisableKeepAlives = true
	resolver := &proxyOnlyResolver{}
	client, err := NewPublicHTTPClient(base, resolver, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := mcpstdio.WithHTTPClient(context.Background(), client)
	root := t.TempDir()
	config := mcpstdio.ServerConfig{Type: "http", URL: "https://example.com/mcp", ConnectorID: "proxy-fixture"}
	tools, err := mcpstdio.ListToolsForServer(ctx, root, "proxy-fixture", config)
	if err != nil || len(tools) != 1 || tools[0].ToolName != "echo" {
		t.Fatalf("actual MCP discovery: tools=%v err=%v", tools, err)
	}
	output, err := mcpstdio.CallToolForServer(ctx, root, "proxy-fixture", "echo", map[string]any{}, config)
	if err != nil || !strings.Contains(output, "proxy execution complete") || toolCalls.Load() != 1 {
		t.Fatalf("actual MCP execution: output=%q calls=%d err=%v", output, toolCalls.Load(), err)
	}
	config.URL = "https://example.com/oauth"
	if _, err := mcpstdio.AuthenticateServer(ctx, root, "proxy-fixture", config); err == nil || !strings.Contains(err.Error(), "registration") || registrationCalls.Load() != 1 {
		t.Fatalf("OAuth metadata did not reach controlled registration boundary: calls=%d err=%v", registrationCalls.Load(), err)
	}
	if localDNSCalls.Load() != 0 || resolver.calls.Load() != 0 || proxyCalls.Load() == 0 {
		t.Fatalf("proxy DNS ownership lost: local=%d directory=%d proxy=%d", localDNSCalls.Load(), resolver.calls.Load(), proxyCalls.Load())
	}
	// Metadata links are checked before registration or opening a browser; the
	// proxy does not make private literals or malformed OAuth endpoints valid.
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		for _, endpoint := range []string{"http://example.com/", "https://localhost/", "https://metadata.internal/", "https://10.0.0.1/", "https://198.18.0.1/", "https://user:secret@example.com/", "https://example.com/#fragment"} {
			config.URL = "https://example.com/oauth?" + url.Values{"field": {field}, "value": {endpoint}}.Encode()
			if _, err := mcpstdio.AuthenticateServer(ctx, root, "proxy-fixture", config); err == nil || strings.Contains(err.Error(), "registration failed") {
				t.Errorf("unsafe metadata %s=%s accepted: %v", field, endpoint, err)
			}
		}
	}
	if registrationCalls.Load() != 1 || localDNSCalls.Load() != 0 || resolver.calls.Load() != 0 {
		t.Fatalf("unsafe metadata advanced or consulted local DNS: registrations=%d local=%d directory=%d", registrationCalls.Load(), localDNSCalls.Load(), resolver.calls.Load())
	}
	before := proxyCalls.Load()
	for _, endpoint := range []string{
		"http://example.com/mcp", "https://localhost/mcp", "https://private.local/mcp",
		"https://10.0.0.1/mcp", "https://198.18.0.1/mcp", "https://[::1]/mcp",
		"https://user:secret@example.com/mcp", "https://example.com/mcp#fragment",
	} {
		config.URL = endpoint
		if _, err := mcpstdio.ListToolsForServer(ctx, root, "proxy-fixture", config); err == nil {
			t.Errorf("unsafe discovery endpoint accepted: %s", endpoint)
		}
		if _, err := mcpstdio.CallToolForServer(ctx, root, "proxy-fixture", "echo", nil, config); err == nil {
			t.Errorf("unsafe execution endpoint accepted: %s", endpoint)
		}
	}
	if proxyCalls.Load() != before {
		t.Fatal("unsafe literal, local host, credentials or fragment reached the proxy")
	}
	config.URL = "https://example.com/redirect"
	if _, err := mcpstdio.ListToolsForServer(ctx, root, "proxy-fixture", config); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("remote MCP redirect accepted: %v", err)
	}
	// A direct directory client must retain private-DNS validation even though
	// it has the same public-transport marker as the explicit-proxy client.
	directBase := *base
	directTransport := base.Transport.(*http.Transport).Clone()
	directTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			return nil, fmt.Errorf("unexpected unpinned destination %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, originURL.Host)
	}
	directBase.Transport = directTransport
	directResolver := &metadataDestinationResolver{}
	directClient, err := NewPublicHTTPClient(&directBase, directResolver, "")
	if err != nil {
		t.Fatal(err)
	}
	directContext := mcpstdio.WithHTTPClient(context.Background(), directClient)
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		config.URL = "https://example.com/oauth?" + url.Values{"field": {field}, "value": {"https://auth.example.net/"}}.Encode()
		if _, err := mcpstdio.AuthenticateServer(directContext, root, "direct-fixture", config); err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("direct private-DNS metadata %s accepted: %v", field, err)
		}
	}
	if directResolver.privateCalls.Load() != 3 || registrationCalls.Load() != 1 {
		t.Fatalf("direct metadata policy lost: private lookups=%d registrations=%d", directResolver.privateCalls.Load(), registrationCalls.Load())
	}
	// An arbitrary supplied HTTP transport, even carrying the same proxy, must
	// not acquire the production transport's public-address/DNS authority.
	proxyURL, _ := url.Parse(proxy.URL)
	base.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyURL)
	config.URL = "https://example.com/mcp"
	before = proxyCalls.Load()
	if _, err := mcpstdio.ListToolsForServer(mcpstdio.WithHTTPClient(context.Background(), base), root, "proxy-fixture", config); err == nil {
		t.Fatal("untrusted client bypassed local public DNS validation")
	}
	if localDNSCalls.Load() == 0 || proxyCalls.Load() != before {
		t.Fatalf("arbitrary client inherited proxy authority: DNS=%d proxy=%d/%d", localDNSCalls.Load(), proxyCalls.Load(), before)
	}

	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store, t.TempDir(), base, proxy.URL)
	defer service.Close(context.Background())
	service.resolver = resolver
	if _, result, err := service.AddDirectory(context.Background(), "owner", AddDirectoryInput{
		Name: "Proxy fixture", URL: "https://example.com/catalog", CatalogUUID: "4f5b9638-2dc3-4b70-bbea-9af732ba57ef",
	}); err != nil || result.Status != "ok" {
		t.Fatalf("actual directory registration: result=%+v err=%v", result, err)
	}
	service.bundled = map[string]bundledConnector{"bundled:proxy-fixture": {
		ID: "bundled:proxy-fixture", Name: "Proxy fixture", AuthRequired: true, OAuthSupported: true,
		Config: mcpstdio.ServerConfig{Type: "http", URL: "https://example.com/oauth"},
	}}
	localBefore, registrationBefore := localDNSCalls.Load(), registrationCalls.Load()
	for _, id := range []string{"directory:proxy-service", "bundled:proxy-fixture"} {
		if _, err := service.AuthorizeUnified(context.Background(), "owner", id); err == nil || !strings.Contains(err.Error(), "registration") {
			t.Fatalf("service OAuth %s did not reach registration boundary: %v", id, err)
		}
	}
	if registrationCalls.Load() != registrationBefore+2 || localDNSCalls.Load() != localBefore || resolver.calls.Load() != 0 {
		t.Fatalf("service discarded proxy authority: registrations=%d/%d localDNS=%d/%d directoryDNS=%d", registrationCalls.Load(), registrationBefore, localDNSCalls.Load(), localBefore, resolver.calls.Load())
	}
	// A raw proxied client is not operator authority, even for a connector that
	// is already stored and was previously reached through the trusted service.
	untrustedService := New(store, t.TempDir(), base)
	defer untrustedService.Close(context.Background())
	untrustedService.resolver = resolver
	untrustedService.bundled = service.bundled
	before, registrationBefore = proxyCalls.Load(), registrationCalls.Load()
	for _, id := range []string{"directory:proxy-service", "bundled:proxy-fixture"} {
		if _, err := untrustedService.AuthorizeUnified(context.Background(), "owner", id); err == nil {
			t.Errorf("service with ambient proxy authorized %s", id)
		}
	}
	if proxyCalls.Load() != before || registrationCalls.Load() != registrationBefore || resolver.calls.Load() != 2 {
		t.Fatalf("untrusted service reached proxy: proxy=%d/%d registration=%d/%d directDNS=%d", proxyCalls.Load(), before, registrationCalls.Load(), registrationBefore, resolver.calls.Load())
	}
}

type metadataDestinationResolver struct{ privateCalls atomic.Int64 }

func (r *metadataDestinationResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if host == "auth.example.net" {
		r.privateCalls.Add(1)
		return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
	}
	if host != "example.com" {
		return nil, fmt.Errorf("unexpected DNS host %s", host)
	}
	return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
}
