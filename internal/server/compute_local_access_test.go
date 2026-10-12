package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestComputeLocalFilesUseExistingOwnerGrantBoundary(t *testing.T) {
	state := t.TempDir()
	store, err := workspace.Open(filepath.Join(state, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := New(Options{Workspace: store, FileRoot: filepath.Join(state, "runtime"),
		XScienceLinkAuth: XScienceLinkAuthOptions{Username: "operator", Password: "fixture-password", UserID: "alice"}})
	owned, outside := t.TempDir(), t.TempDir()
	for _, root := range []string{owned, outside} {
		if err := os.WriteFile(filepath.Join(root, "result.txt"), []byte("private result"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := srv.upsertHostGrant("alice", owned, "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "alice", Name: "Fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "OPERON", Status: "processing", ConversationType: "chat"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.registerComputeWorkbenchRoutes(mux)
	for _, tc := range []struct {
		name, owner, root string
		want              int
	}{
		{"owned", "alice", owned, http.StatusOK},
		{"foreign-owner", "bob", owned, http.StatusForbidden},
		{"ungranted", "alice", outside, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{
				"/api/compute/local/files?path=" + urlQueryEscape(tc.root),
				"/api/compute/local/download?path=" + urlQueryEscape(filepath.Join(tc.root, "result.txt")),
			} {
				response := requestCompute(t, mux, http.MethodGet, target, nil, tc.owner)
				if response.Code != tc.want {
					t.Fatalf("%s status=%d want=%d: %s", target, response.Code, tc.want, response.Body.String())
				}
			}
		})
	}
	deniedImport := requestCompute(t, mux, http.MethodPost, "/api/compute/local/import", map[string]any{
		"path": filepath.Join(outside, "result.txt"), "projectId": "project",
	}, "alice")
	if deniedImport.Code != http.StatusForbidden {
		t.Fatalf("ungranted import=%d: %s", deniedImport.Code, deniedImport.Body.String())
	}
	if err := os.Symlink(outside, filepath.Join(owned, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/api/compute/local/files?path=" + urlQueryEscape(filepath.Join(owned, "escape")),
		"/api/compute/local/download?path=" + urlQueryEscape(filepath.Join(owned, "escape", "result.txt")),
	} {
		response := requestCompute(t, mux, http.MethodGet, target, nil, "alice")
		if response.Code == http.StatusOK {
			t.Fatalf("symlink escaped owner scope: %s", target)
		}
	}
}
