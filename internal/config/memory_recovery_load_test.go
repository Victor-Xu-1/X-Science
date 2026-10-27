package config

import (
	"os"
	"path/filepath"
	"testing"

	"x-science/internal/memoryconfig"
)

func TestMemoryRecoveryLoadKeepsExistingFilesStrict(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		want      memoryconfig.ForkedViewRecovery
	}{
		{"missing-memory-object", `{}`, memoryconfig.ForkedViewRecoveryStrict},
		{"missing-policy", `{"memory":{"enabled":true}}`, memoryconfig.ForkedViewRecoveryStrict},
		{"explicit-auto", `{"memory":{"forked_view_recovery":"compact-on-oversize"}}`, memoryconfig.ForkedViewRecoveryCompact},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.raw), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("X_SCIENCE_CONFIG", path)
			cfg, err := Load()
			if err != nil || cfg.Memory.ForkedViewRecovery != test.want {
				t.Fatalf("policy=%s err=%v", cfg.Memory.ForkedViewRecovery, err)
			}
		})
	}
}
