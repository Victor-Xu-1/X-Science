package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/config"
	"x-science/internal/networktls"
)

func TestLoadedConfigurationDoesNotPromoteAmbientPublicSourceProxy(t *testing.T) {
	for _, authority := range []string{"ambient", "product-environment", "persisted-setting"} {
		t.Run(authority, func(t *testing.T) {
			var proxyCalls, directCalls atomic.Int64
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				proxyCalls.Add(1)
				if request.Method != http.MethodConnect || request.Host != "93.184.216.34:443" {
					t.Errorf("unexpected proxy destination: %s %s", request.Method, request.Host)
				}
				http.Error(w, "controlled proxy failure", http.StatusServiceUnavailable)
			}))
			defer proxy.Close()
			root := t.TempDir()
			t.Setenv("X_SCIENCE_HOME", root)
			t.Setenv("X_SCIENCE_CONFIG", "")
			t.Setenv("X_SCIENCE_NETWORK_PROXY", "")
			for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
				t.Setenv(key, "")
			}
			t.Setenv("HTTPS_PROXY", proxy.URL)
			if authority == "product-environment" {
				t.Setenv("X_SCIENCE_NETWORK_PROXY", proxy.URL)
			} else if authority == "persisted-setting" {
				data, _ := json.Marshal(map[string]any{"network": map[string]any{"proxy": proxy.URL}})
				path := filepath.Join(root, "configured.json")
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("X_SCIENCE_CONFIG", path)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			// Match the command entrypoint: its provider/runtime client still uses
			// the effective route, but that alone grants no public-source authority.
			base, err := networktls.HTTPClient(http.DefaultClient, "", cfg.Network.Proxy)
			if err != nil {
				t.Fatal(err)
			}
			proxyURL, _ := url.Parse(proxy.URL)
			transport := base.Transport.(*http.Transport)
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == proxyURL.Host {
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
				}
				directCalls.Add(1)
				return nil, errors.New("controlled direct destination; no external network")
			}
			app := New(Options{FileRoot: root, HTTPClient: base,
				ConfigNetworkProxy: cfg.Network.Proxy, ConfigNetworkProxyInherited: cfg.Network.ProxyInherited})
			t.Cleanup(func() { _ = app.Close(context.Background()) })
			if app.configNetworkProxy != proxy.URL {
				t.Fatal("effective runtime route was changed")
			}
			client, err := app.publicSourceHTTPClient(context.Background(), "https://93.184.216.34/", app.httpClient)
			if err != nil {
				t.Fatal(err)
			}
			client.Timeout = time.Second
			if response, err := client.Get("https://93.184.216.34/"); err == nil {
				response.Body.Close()
				t.Fatal("fixture should return a controlled routing error")
			}
			if authority == "ambient" {
				if proxyCalls.Load() != 0 || directCalls.Load() != 1 || app.publicSourceProxy != "" {
					t.Fatal("config.Load promoted ambient proxy into public-source authority")
				}
			} else if proxyCalls.Load() != 1 || directCalls.Load() != 0 || app.publicSourceProxy != proxy.URL {
				t.Fatal("explicit operator proxy lost authority or silently used a direct route")
			}
		})
	}
}
