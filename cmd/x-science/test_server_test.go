package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"x-science/internal/server"
)

// newXScienceTestServer mirrors the source deployment wiring used by the
// backend watcher. Tests that exercise HTTP readiness must load the real
// v1.1 skills and signed bundled agent catalog; an empty Options value is a
// valid unit fixture, but it is not a ready scientific runtime.
func newXScienceTestServer(t *testing.T, options server.Options) *server.Server {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if len(options.SkillDirectories) == 0 {
		options.SkillDirectories = []string{filepath.Join(repoRoot, "skills")}
	}
	if options.AgentCatalog == nil && options.AgentCatalogRoot == "" && options.AgentManifestPath == "" {
		options.AgentCatalogRoot = filepath.Join(repoRoot, "assets", "x-science", "agents")
		options.AgentManifestPath = filepath.Join(repoRoot, "assets", "x-science", "agents.manifest.json")
	}
	return server.New(options)
}
