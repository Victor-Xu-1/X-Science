package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestServerPublicSourceConsumersKeepExplicitProxy(t *testing.T) {
	var requests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodConnect || request.Host != "source.example.com:443" {
			t.Errorf("unexpected proxy destination: %s %s", request.Method, request.Host)
		}
		requests.Add(1)
		http.Error(w, "controlled proxy fixture", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	app := New(Options{FileRoot: t.TempDir(), ConfigNetworkProxy: proxy.URL})
	// Registration must use the same DNS ownership as later discovery/execution.
	name := "source-fixture"
	for _, destination := range []string{"https://source.example.com/", "https://127.0.0.1/private"} {
		wantError := destination != "https://source.example.com/"
		_, err := app.prepareWebMCPRecord(context.Background(), "owner", "test-connector", webMCPMutationRequest{
			Name: &name, Transport: &webMCPTransport{Type: "streamable_http", URL: destination},
		})
		if (err != nil) != wantError {
			t.Fatalf("bridge registration destination=%s err=%v", destination, err)
		}
		err = app.validateCompatMCPServerInput(context.Background(), name, destination, "streamable_http", "")
		if (err != nil) != wantError {
			t.Fatalf("compat registration destination=%s err=%v", destination, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("URL registration unexpectedly contacted the proxy")
	}
	for name, factory := range map[string]func(context.Context, string, *http.Client) (*http.Client, error){
		"custom MCP": app.publicSourceHTTPClient,
		"speech":     app.speechClientForURL,
		"image":      app.webImageClientForURL,
	} {
		t.Run(name, func(t *testing.T) {
			before := requests.Load()
			client, err := factory(context.Background(), "https://source.example.com/", app.httpClient)
			if err != nil {
				t.Fatal(err)
			}
			if response, err := client.Get("https://source.example.com/"); err == nil {
				response.Body.Close()
				t.Fatal("fixture should report its controlled proxy failure")
			}
			if requests.Load() != before+1 {
				t.Fatal("consumer lost explicit proxy authority")
			}
			if _, err := factory(context.Background(), "https://127.0.0.1/private", app.httpClient); err == nil {
				t.Fatal("explicit proxy bypassed private URL policy")
			}
		})
	}
}
