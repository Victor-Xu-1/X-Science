package server

import (
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func newWebFSProjectTestServer(
	t *testing.T,
	stateRoot string,
	projectRoot string,
	userID string,
) *Server {
	t.Helper()
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "web-fs-project", UserID: userID, Name: "Web FS", Path: projectRoot,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{FileRoot: filepath.Join(stateRoot, "runtime"), Workspace: store})
	if _, err := srv.upsertHostGrant(userID, projectRoot, "read_write"); err != nil {
		t.Fatal(err)
	}
	return srv
}

func writeWebFSTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
