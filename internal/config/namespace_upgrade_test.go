package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNamespaceConfigUpgradePreservesOperatorValues(t *testing.T) {
	raw := []byte(`{"synon_link_auth":{"username":"owner","password":"opaque"},"synon_link_package":"assets/synon-link/x-science-browser-extension-v0.6.10.zip","home_dir":"/user/synon-data","custom":{"synon_link_auth":"untouched"}}`)
	upgraded, err := upgradeConfigNamespace(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(upgraded, &got); err != nil {
		t.Fatal(err)
	}
	if _, found := got["synon_link_auth"]; found || got["x_science_link_auth"].(map[string]any)["username"] != "owner" {
		t.Fatal("owned config field was not transferred")
	}
	if got["home_dir"] != "/user/synon-data" || got["custom"].(map[string]any)["synon_link_auth"] != "untouched" {
		t.Fatal("user-owned config data changed")
	}
	if got["x_science_link_package"] != "assets/x-science-link/x-science-browser-extension-v0.6.11.zip" {
		t.Fatal("exact bundled connector identity was not upgraded")
	}
	again, err := upgradeConfigNamespace(upgraded)
	if err != nil || string(again) != string(upgraded) {
		t.Fatal("config upgrade was not idempotent")
	}
}

func TestNamespaceEnvironmentSpecialPrefixesShareOneCanonicalAuthority(t *testing.T) {
	for _, prefix := range []string{"SYNON_AI_", "SYNON_BIOMED_"} {
		t.Run(prefix, func(t *testing.T) {
			old, current := prefix+"PREFIX_TEST", "X_SCIENCE_PREFIX_TEST"
			t.Setenv(old, "opaque operator value")
			t.Setenv(current, "")
			if err := os.Unsetenv(current); err != nil {
				t.Fatal(err)
			}
			if err := UpgradeProcessEnvironment(); err != nil {
				t.Fatal(err)
			}
			if os.Getenv(current) != "opaque operator value" {
				t.Fatal("special prefix did not use canonical key")
			}
			if _, found := os.LookupEnv(old); found {
				t.Fatal("historical process key remains")
			}
		})
	}
	first, second, current := "SYNON_AI_CONFLICT_TEST", "SYNON_BIOMED_CONFLICT_TEST", "X_SCIENCE_CONFLICT_TEST"
	t.Setenv(first, "first-value")
	t.Setenv(second, "different-value")
	t.Setenv(current, "")
	if err := os.Unsetenv(current); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeProcessEnvironment(); err == nil {
		t.Fatal("ambiguous historical authority accepted")
	}
	if os.Getenv(first) != "first-value" || os.Getenv(second) != "different-value" {
		t.Fatal("conflicting inputs changed")
	}
	if _, found := os.LookupEnv(current); found {
		t.Fatal("preflight conflict partially installed new authority")
	}
}

func TestNamespaceConfigUpgradeRejectsConflictingAuthority(t *testing.T) {
	_, err := upgradeConfigNamespace([]byte(`{"synon_link_auth":{"username":"first"},"x_science_link_auth":{"username":"other"}}`))
	if err == nil {
		t.Fatal("conflicting operator settings accepted")
	}
	for _, value := range []string{`null`, `[]`, `"config"`, `{`} {
		if _, err := upgradeConfigNamespace([]byte(value)); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	for _, path := range []string{"/user/synon-link/custom.zip", "assets/custom.zip"} {
		value, _ := json.Marshal(path)
		raw := append([]byte(`{"synon_link_package":`), value...)
		raw = append(raw, '}')
		upgraded, err := upgradeConfigNamespace(raw)
		if err != nil || !strings.Contains(string(upgraded), string(value)) {
			t.Fatal("operator connector path changed")
		}
	}
}

func TestNamespaceEnvironmentUpgradePreservesOpaqueValuesAndRejectsConflict(t *testing.T) {
	const previous, current = "SYNON_NAMESPACE_TEST", "X_SCIENCE_NAMESPACE_TEST"
	t.Setenv(previous, "opaque operator bytes")
	t.Setenv(current, "")
	if err := os.Unsetenv(current); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeProcessEnvironment(); err != nil {
		t.Fatal(err)
	}
	if _, found := os.LookupEnv(previous); found || os.Getenv(current) != "opaque operator bytes" {
		t.Fatal("environment transfer lost its original value")
	}
	if err := UpgradeProcessEnvironment(); err != nil {
		t.Fatal("environment transfer was not idempotent")
	}
	t.Setenv(previous, "other opaque bytes")
	if err := UpgradeProcessEnvironment(); err == nil {
		t.Fatal("conflicting canonical environment accepted")
	}
	if os.Getenv(current) != "opaque operator bytes" || os.Getenv(previous) != "other opaque bytes" {
		t.Fatal("conflicting environment was modified")
	}
}
