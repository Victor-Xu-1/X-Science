package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// UpgradeProcessEnvironment is a one-time bootstrap transfer of operator-owned
// environment keys. It is not a second config authority: after successful
// validation only canonical keys remain in this process and its children.
// Values (including credentials and user paths) are never changed or logged.
func UpgradeProcessEnvironment() error {
	type transfer struct{ from, to, value string }
	var transfers []transfer
	targetValues := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "SYNON_") {
			continue
		}
		to := "X_SCIENCE_" + strings.TrimPrefix(key, "SYNON_")
		for _, prefix := range []string{"SYNON_AI_", "SYNON_BIOMED_"} {
			if strings.HasPrefix(key, prefix) {
				to = "X_SCIENCE_" + strings.TrimPrefix(key, prefix)
				break
			}
		}
		if current, found := os.LookupEnv(to); found && current != value {
			return fmt.Errorf("namespace upgrade conflicts with configured %s", to)
		}
		if previous, found := targetValues[to]; found && previous != value {
			return fmt.Errorf("namespace upgrade has conflicting inputs for %s", to)
		}
		targetValues[to] = value
		transfers = append(transfers, transfer{key, to, value})
	}
	for _, item := range transfers {
		if err := os.Setenv(item.to, item.value); err != nil {
			return fmt.Errorf("set canonical environment key %s: %w", item.to, err)
		}
	}
	for _, item := range transfers {
		if value, found := os.LookupEnv(item.to); !found || value != item.value {
			return errors.New("namespace environment transfer verification failed")
		}
		if err := os.Unsetenv(item.from); err != nil {
			return errors.New("remove transferred environment key failed")
		}
	}
	return nil
}

// upgradeConfigNamespace consumes only the published legacy config fields.
// This explicit migration input is historical evidence, never emitted branding.
// Arbitrary nested/user strings are not searched or rewritten.
func upgradeConfigNamespace(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("config must be a JSON object")
	}
	changed := false
	for old, current := range map[string]string{
		"synon_link_auth":    "x_science_link_auth",
		"synon_link_package": "x_science_link_package",
	} {
		value, found := fields[old]
		if !found {
			continue
		}
		if existing, found := fields[current]; found && !sameConfigJSON(existing, value) {
			return nil, fmt.Errorf("namespace upgrade conflicts with configured %s", current)
		}
		fields[current] = value
		delete(fields, old)
		changed = true
	}
	if value, found := fields["x_science_link_package"]; found {
		var path string
		if json.Unmarshal(value, &path) == nil {
			switch path {
			case "assets/synon-link/synon-link-extension-v0.6.10.zip", "assets/synon-link/x-science-browser-extension-v0.6.10.zip":
				fields["x_science_link_package"], _ = json.Marshal("assets/x-science-link/x-science-browser-extension-v0.6.11.zip")
				changed = true
			}
		}
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(fields)
}

func sameConfigJSON(left, right []byte) bool {
	if bytes.Equal(left, right) {
		return true
	}
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
