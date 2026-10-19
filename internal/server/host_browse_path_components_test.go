package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestCompatibilityHostBrowseLiteralDots(t *testing.T) {
	s, granted, _ := compatibilityHostBrowsePathFixture(t)
	for _, name := range []string{"analysis..archive", "..metadata", "version..", "...", "regular"} {
		t.Run(name, func(t *testing.T) {
			directory := filepath.Join(granted, name)
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "fixture.txt"), []byte("isolated fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			entries := compatibilityHostBrowseJSON(t, s.Handler(), "/api/preferences/host-browse?path="+url.QueryEscape(directory), "owner", http.StatusOK).([]any)
			if len(entries) != 1 || entries[0].(map[string]any)["name"] != "fixture.txt" {
				t.Fatalf("literal dot directory entries = %#v", entries)
			}
		})
	}
	// Literal dots are also valid in the granted root itself, not only its children.
	dottedRoot := filepath.Join(t.TempDir(), "granted..archive")
	if err := os.MkdirAll(filepath.Join(dottedRoot, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.upsertHostGrant("owner", dottedRoot, "read"); err != nil {
		t.Fatal(err)
	}
	entries := compatibilityHostBrowseJSON(t, s.Handler(), "/api/preferences/host-browse?path="+url.QueryEscape(dottedRoot), "owner", http.StatusOK).([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["name"] != "child" {
		t.Fatalf("dotted granted root entries = %#v", entries)
	}
}

func TestCompatibilityHostBrowseParentComponents(t *testing.T) {
	s, granted, _ := compatibilityHostBrowsePathFixture(t)
	separator := string(filepath.Separator)
	// Do not use filepath.Join here: it would erase the raw parent component
	// before the HTTP boundary gets the request under test.
	for _, suffix := range []string{separator + "..", separator + "child" + separator + "..", separator + "child" + separator + ".." + separator + "child"} {
		requested := granted + suffix
		response := compatibilityHostBrowseJSON(t, s.Handler(), "/api/preferences/host-browse?path="+url.QueryEscape(requested), "owner", http.StatusBadRequest).(map[string]any)
		if response["detail"] != "absolute path required" {
			t.Fatalf("raw parent component reached canonical directory reading: %#v", response)
		}
	}
}

func TestCompatibilityHostBrowseGrantBoundary(t *testing.T) {
	s, granted, outside := compatibilityHostBrowsePathFixture(t)
	for _, request := range []struct {
		path string
		user string
	}{{granted, "foreign"}, {outside, "owner"}} {
		response := compatibilityHostBrowseJSON(t, s.Handler(), "/api/preferences/host-browse?path="+url.QueryEscape(request.path), request.user, http.StatusBadRequest).(map[string]any)
		if response["detail"] != "Directory is not under $HOME or a granted root." {
			t.Fatalf("owned-grant boundary changed: %#v", response)
		}
	}
	alias := filepath.Join(granted, "escape..link")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlink fixture is unavailable: %v", err)
	}
	response := compatibilityHostBrowseJSON(t, s.Handler(), "/api/preferences/host-browse?path="+url.QueryEscape(alias), "owner", http.StatusBadRequest).(map[string]any)
	if response["detail"] != "Directory is not under $HOME or a granted root." {
		t.Fatalf("resolved symlink escaped the existing grant boundary: %#v", response)
	}
}

func compatibilityHostBrowsePathFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := New(Options{FileRoot: t.TempDir()})
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	root := t.TempDir()
	granted := filepath.Join(root, "owned")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{filepath.Join(granted, "child"), outside} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.upsertHostGrant("owner", granted, "read"); err != nil {
		t.Fatal(err)
	}
	return s, granted, outside
}
