package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadKeepsExplicitProxyAuthoritySeparateFromAmbientRoute(t *testing.T) {
	for _, test := range []struct {
		name, environmentKey, ambient, explicit, persisted, want string
		inherited                                                bool
	}{
		{name: "none"},
		{name: "ambient-https", environmentKey: "HTTPS_PROXY", ambient: "http://Proxy.Example:8080", want: "http://proxy.example:8080", inherited: true},
		{name: "ambient-lowercase", environmentKey: "https_proxy", ambient: "http://proxy.example:8080", want: "http://proxy.example:8080", inherited: true},
		{name: "ambient-http", environmentKey: "HTTP_PROXY", ambient: "http://proxy.example:8080", want: "http://proxy.example:8080", inherited: true},
		{name: "ambient-http-lowercase", environmentKey: "http_proxy", ambient: "http://proxy.example:8080", want: "http://proxy.example:8080", inherited: true},
		{name: "invalid-ambient", environmentKey: "HTTPS_PROXY", ambient: "socks5://proxy.example:1080"},
		{name: "explicit-environment", environmentKey: "HTTPS_PROXY", ambient: "http://ambient.example:8080", explicit: "http://Product.Example:3128", want: "http://product.example:3128"},
		{name: "persisted-explicit", environmentKey: "HTTPS_PROXY", ambient: "http://ambient.example:8080", persisted: "http://Configured.Example:3128", want: "http://configured.example:3128"},
		{name: "environment-overrides-file", explicit: "http://product.example:3128", persisted: "http://configured.example:8080", want: "http://product.example:3128"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("X_SCIENCE_HOME", filepath.Join(root, "runtime"))
			t.Setenv("X_SCIENCE_CONFIG", "")
			t.Setenv("X_SCIENCE_NETWORK_PROXY", test.explicit)
			for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
				t.Setenv(key, "")
			}
			if test.environmentKey != "" {
				t.Setenv(test.environmentKey, test.ambient)
			}
			configPath := filepath.Join(root, "config.json")
			if test.persisted != "" {
				data, _ := json.Marshal(map[string]any{"network": map[string]any{"proxy": test.persisted}})
				if err := os.WriteFile(configPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("X_SCIENCE_CONFIG", configPath)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Network.Proxy != test.want || cfg.Network.ProxyInherited != test.inherited {
				t.Fatalf("proxy=%q inherited=%t want=%q/%t", cfg.Network.Proxy, cfg.Network.ProxyInherited, test.want, test.inherited)
			}
			if err := applyNetworkPolicy(&cfg, configPath); err != nil || cfg.Network.ProxyInherited != test.inherited {
				t.Fatalf("reapplying policy changed provenance: inherited=%t err=%v", cfg.Network.ProxyInherited, err)
			}
			serialized, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip Config
			if err := json.Unmarshal(serialized, &roundtrip); err != nil {
				t.Fatal(err)
			}
			persistedProxy := test.want
			if test.inherited {
				persistedProxy = ""
			}
			if roundtrip.Network.Proxy != persistedProxy {
				t.Fatalf("serialized proxy=%q want=%q; ambient route must not become an explicit setting", roundtrip.Network.Proxy, persistedProxy)
			}
			if err := os.WriteFile(configPath, serialized, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("X_SCIENCE_CONFIG", configPath)
			t.Setenv("X_SCIENCE_NETWORK_PROXY", "")
			reloaded, err := Load()
			if err != nil || reloaded.Network.Proxy != test.want || reloaded.Network.ProxyInherited != test.inherited {
				t.Fatalf("reload proxy=%q inherited=%t err=%v", reloaded.Network.Proxy, reloaded.Network.ProxyInherited, err)
			}
			for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
				t.Setenv(key, "")
			}
			withoutAmbient, err := Load()
			if err != nil || withoutAmbient.Network.Proxy != persistedProxy || withoutAmbient.Network.ProxyInherited {
				t.Fatalf("removing ambient route after save: proxy=%q inherited=%t err=%v", withoutAmbient.Network.Proxy, withoutAmbient.Network.ProxyInherited, err)
			}
		})
	}
}

func TestNetworkProxySerializationPreservesUnrelatedPolicy(t *testing.T) {
	network := NetworkPolicyConfig{
		ResponseHeaderTimeoutSeconds: 45, ReadIdleTimeoutSeconds: 30,
		TransferIdleTimeoutSeconds: 60, SearchTimeoutSeconds: 90,
		AllowedDomains: []string{"example.com"}, DeniedDomains: []string{"blocked.example.com"},
		CABundle: "/configured/ca.pem", MCPX509Strict: "strict",
		Proxy: "http://ambient.example:8080", ProxyInherited: true,
	}
	raw, err := json.Marshal(network)
	if err != nil {
		t.Fatal(err)
	}
	var got NetworkPolicyConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	network.Proxy = ""
	network.ProxyInherited = false
	if !reflect.DeepEqual(got, network) {
		t.Fatalf("proxy provenance changed unrelated network policy: got=%+v want=%+v", got, network)
	}
}
