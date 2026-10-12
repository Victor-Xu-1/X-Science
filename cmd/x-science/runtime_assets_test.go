package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverXScienceRuntimeAssetsDirUsesReleaseLayout(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "assets", "optional")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("X_SCIENCE_RUNTIME_ASSETS_DIR", "")
	got := discoverXScienceRuntimeAssetsDir([]string{root})
	assertSamePath(t, got, want)
}

func TestDiscoverXScienceRuntimeAssetsDirHonorsValidConfiguredDirectory(t *testing.T) {
	root := t.TempDir()
	fallback := filepath.Join(root, "assets", "optional")
	configured := filepath.Join(root, "custom-assets")
	for _, directory := range []string{fallback, configured} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("X_SCIENCE_RUNTIME_ASSETS_DIR", configured)
	got := discoverXScienceRuntimeAssetsDir([]string{root})
	assertSamePath(t, got, configured)
}

func TestDiscoverXScienceRuntimeAssetsDirRejectsMissingConfiguredDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("X_SCIENCE_RUNTIME_ASSETS_DIR", filepath.Join(root, "missing"))
	if got := discoverXScienceRuntimeAssetsDir([]string{root}); got != "" {
		t.Fatalf("runtime assets dir = %q", got)
	}
}

func assertSamePath(t *testing.T, got, want string) {
	t.Helper()
	gotPath, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
}
