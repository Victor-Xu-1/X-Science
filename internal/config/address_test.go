package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeHostRejectsMalformedBracketBindings(t *testing.T) {
	for _, host := range []string{"[]", "[ ]", "[::1", "::1]", "[[::1]]", "[localhost]", "[127.0.0.1]"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			raw, err := json.Marshal(map[string]any{"host": host, "port": 18886})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("X_SCIENCE_HOME", root)
			t.Setenv("X_SCIENCE_DATA_DIR_CONTROL", filepath.Join(root, "control.json"))
			t.Setenv("X_SCIENCE_CONFIG", path)
			t.Setenv("X_SCIENCE_ADDRESS", "")
			t.Setenv("X_SCIENCE_HOST", "")
			t.Setenv("X_SCIENCE_PORT", "")
			t.Setenv("X_SCIENCE_LINK_AUTH_PASSWORD", "owned-test-authentication")
			t.Setenv("X_SCIENCE_AUTH_APPLE_PRIVATE_KEY_FILE", "")
			if err := os.Unsetenv("X_SCIENCE_AUTH_APPLE_PRIVATE_KEY_FILE"); err != nil {
				t.Fatal(err)
			}
			if cfg, err := Load(); err == nil || !strings.Contains(err.Error(), "runtime host") {
				t.Fatalf("malformed host %q was not rejected by host validation: address=%q err=%v", host, cfg.Address(), err)
			}
		})
	}
	for _, host := range []string{"[::1]", "[fe80::1%eth0]", "localhost", "127.0.0.1", "::1"} {
		t.Run("valid_"+host, func(t *testing.T) {
			if _, err := normalizedRuntimeHost(host); err != nil {
				t.Fatalf("valid host %q was rejected: %v", host, err)
			}
		})
	}
	if err := validateRuntimeBinding(Config{Host: "::"}); err == nil || !strings.Contains(err.Error(), "requires X_SCIENCE_LINK_AUTH_PASSWORD") {
		t.Fatalf("unauthenticated wildcard lost the existing authentication guard: %v", err)
	}
}

func TestAddressPreservesInvalidHostsWithoutWideningBinding(t *testing.T) {
	for _, host := range []string{"[]", "[ ]", "[::1", "::1]", "[[::1]]", " ", "127.0.0.1:0", ""} {
		t.Run(host, func(t *testing.T) {
			address := (Config{Host: host, Port: 18886}).Address()
			if _, _, err := net.SplitHostPort(address); err == nil {
				t.Errorf("invalid host became a usable host/port binding: %q", address)
			}
			// Parse failure alone is insufficient: Listen accepts the empty
			// address and creates an ephemeral wildcard listener.
			listener, err := net.Listen("tcp", address)
			if listener != nil {
				_ = listener.Close()
			}
			if err == nil {
				t.Fatalf("invalid host %q started a listener using %q", host, address)
			}
		})
	}
}

func TestAddressJoinsIPv4IPv6AndBracketedBindings(t *testing.T) {
	for _, test := range []struct {
		host string
		want string
	}{
		{"127.0.0.1", "127.0.0.1:18886"},
		{"localhost", "localhost:18886"},
		{"::1", "[::1]:18886"},
		{"[::1]", "[::1]:18886"},
		{"::", "[::]:18886"},
		{"fe80::1%eth0", "[fe80::1%eth0]:18886"},
	} {
		t.Run(test.host, func(t *testing.T) {
			if got := (Config{Host: test.host, Port: 18886}).Address(); got != test.want {
				t.Fatalf("Address() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAddressUsesLoadedConfigAndEnvironmentPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, address, host, port, want string
	}{
		{name: "config only", want: "127.0.0.1:18886"},
		{name: "address override", address: "127.0.0.2:18887", want: "127.0.0.2:18887"},
		{name: "host and port overrides", address: "127.0.0.2:18887", host: "127.0.0.3", port: "18888", want: "127.0.0.3:18888"},
		{name: "IPv6 address override", address: "[::1]:18889", want: "[::1]:18889"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			if err := os.WriteFile(path, []byte(`{"host":"127.0.0.1","port":18886}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("X_SCIENCE_HOME", root)
			t.Setenv("X_SCIENCE_DATA_DIR_CONTROL", filepath.Join(root, "control.json"))
			t.Setenv("X_SCIENCE_CONFIG", path)
			t.Setenv("X_SCIENCE_ADDRESS", test.address)
			t.Setenv("X_SCIENCE_HOST", test.host)
			t.Setenv("X_SCIENCE_PORT", test.port)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Address(); got != test.want {
				t.Fatalf("loaded Address() = %q, want %q", got, test.want)
			}
		})
	}
}
