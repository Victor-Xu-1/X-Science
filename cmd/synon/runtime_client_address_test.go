package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"synon-go/internal/config"
)

func TestBrowserLoginURLRetainsWildcardAddressFamily(t *testing.T) {
	for _, test := range []struct{ host, want string }{
		{"0.0.0.0", "http://127.0.0.1:18886/#/login"},
		{"::", "http://[::1]:18886/#/login"},
		{"[::]", "http://[::1]:18886/#/login"},
		{"0:0:0:0:0:0:0:0", "http://[::1]:18886/#/login"},
		{"::1", "http://[::1]:18886/#/login"},
		{"127.0.0.2", "http://127.0.0.2:18886/#/login"},
	} {
		t.Run(test.host, func(t *testing.T) {
			if got := browserLoginURL(config.Config{Host: test.host, Port: 18886}); got != test.want {
				t.Fatalf("browserLoginURL=%q, want %q", got, test.want)
			}
		})
	}
}

func TestDoctorBaseURLRetainsWildcardAddressFamily(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		case "/api/tools/AgentRuntimeDoctor/execute":
			_, _ = w.Write([]byte(`{"result":{"ok":true,"scope":"mcp"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	root := t.TempDir()
	t.Setenv("SYNON_HOME", root)
	t.Setenv("SYNON_DATA_DIR_CONTROL", filepath.Join(root, "control.json"))
	t.Setenv("SYNON_CONFIG", "")
	t.Setenv("SYNON_ADDRESS", "")
	t.Setenv("SYNON_HOST", "::")
	t.Setenv("SYNON_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	t.Setenv("SYNON_BASE_URL", "")
	t.Setenv("SYNON_LINK_AUTH_PASSWORD", "owned-test-authentication")
	t.Setenv("SYNON_AUTH_APPLE_PRIVATE_KEY_FILE", "")
	if err := os.Unsetenv("SYNON_AUTH_APPLE_PRIVATE_KEY_FILE"); err != nil {
		t.Fatal(err)
	}
	base, err := resolveDoctorBaseURL("")
	if err != nil || base != fixture.URL {
		t.Fatalf("doctor base=%q want=%q err=%v", base, fixture.URL, err)
	}
	var output bytes.Buffer
	if err := runDoctorCLI(context.Background(), []string{"--scope", "mcp"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"ok": true`) {
		t.Fatalf("IPv6-only doctor fixture was not reached: %s", output.String())
	}
}
