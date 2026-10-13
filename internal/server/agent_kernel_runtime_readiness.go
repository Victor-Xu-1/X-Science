package server

import (
	"os"
	"strings"
)

// Runtime admission is distinct from package installation. This read-only
// check uses the same protected-root authority as execution and never creates
// directories during a health request.
func (s *Server) agentKernelManagedDirectoriesReady() bool {
	if s == nil {
		return false
	}
	if _, err := s.agentKernelProtectedPaths(); err != nil {
		return false
	}
	for _, path := range []string{s.condaHome, s.condaEnvsPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func agentKernelRuntimeConfigurationPreflight() map[string]any {
	return map[string]any{
		"ok": false, "executed": false, "preflight": true,
		"status":    "runtime_configuration_preflight_required",
		"code":      "kernel_runtime_directories_unavailable",
		"message":   "The installed analysis runtime cannot be admitted with the current directory configuration.",
		"recovery":  "Repair the configured runtime directories before retrying execution. Changing code or creating another environment does not repair this shared configuration. Preserve completed results and continue independent work.",
		"retryable": false, "failure_scope": "runtime_configuration",
	}
}
