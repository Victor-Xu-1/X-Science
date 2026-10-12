package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const legacyDefaultHomeBasename = ".synon-go"

func legacyDefaultHome() string {
	if runtime.GOOS == "windows" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Join(home, legacyDefaultHomeBasename)
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, legacyDefaultHomeBasename)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, legacyDefaultHomeBasename)
	}
	return legacyDefaultHomeBasename
}

// LegacyWorkspaceHomeRoots returns installation homes that may still own the
// published v1.1 workspace database. When the resolved home uses the canonical
// product basename, the sibling historical default home is also searched.
func LegacyWorkspaceHomeRoots(resolvedHome string) []string {
	roots := []string{resolvedHome}
	legacy := legacySiblingInstallationHome(resolvedHome)
	if legacy == "" || filepath.Clean(legacy) == filepath.Clean(resolvedHome) {
		return roots
	}
	return append(roots, legacy)
}

func legacySiblingInstallationHome(canonicalHome string) string {
	cleaned := filepath.Clean(strings.TrimSpace(canonicalHome))
	if cleaned == "" || cleaned == "." {
		return ""
	}
	if filepath.Base(cleaned) != filepath.Base(defaultHome()) {
		return ""
	}
	return filepath.Join(filepath.Dir(cleaned), legacyDefaultHomeBasename)
}
