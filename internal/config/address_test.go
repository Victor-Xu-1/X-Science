package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
			t.Setenv("SYNON_HOME", root)
			t.Setenv("SYNON_DATA_DIR_CONTROL", filepath.Join(root, "control.json"))
			t.Setenv("SYNON_CONFIG", path)
			t.Setenv("SYNON_ADDRESS", test.address)
			t.Setenv("SYNON_HOST", test.host)
			t.Setenv("SYNON_PORT", test.port)
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
