package memoryconfig

import (
	"encoding/json"
	"testing"
)

func TestForkedViewRecoveryConfigurationDistinguishesLegacyAndExplicit(t *testing.T) {
	if Default().ForkedViewRecovery != ForkedViewRecoveryCompact {
		t.Fatal("new default does not explicitly record recovery")
	}
	for _, test := range []struct {
		name, raw string
		want      ForkedViewRecovery
		invalid   bool
	}{
		{"missing", `{}`, ForkedViewRecoveryStrict, false},
		{"old-forked", `{"extract_mode":"forked"}`, ForkedViewRecoveryStrict, false},
		{"strict", `{"forked_view_recovery":"strict"}`, ForkedViewRecoveryStrict, false},
		{"explicit", `{"forked_view_recovery":"compact-on-oversize"}`, ForkedViewRecoveryCompact, false},
		{"null", `{"forked_view_recovery":null}`, "", true},
		{"unknown", `{"forked_view_recovery":"any-error"}`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var cfg Config
			err := json.Unmarshal([]byte(test.raw), &cfg)
			if test.invalid {
				if err == nil {
					t.Fatal("invalid recovery policy accepted")
				}
				return
			}
			if err != nil || cfg.ForkedViewRecovery != test.want {
				t.Fatalf("policy=%s err=%v", cfg.ForkedViewRecovery, err)
			}
		})
	}
}
