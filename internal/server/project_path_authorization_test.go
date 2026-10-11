package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestProjectCreateRejectsUngrantedHostPath(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	if err := os.MkdirAll(protectedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	ungranted := t.TempDir()
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{FileRoot: protectedRoot, Workspace: store})
	handler := app.Handler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "owned", "name": "Owned", "path": ungranted,
	}))))
	if response.Code != http.StatusForbidden {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProjectCreateAllowsManagedAndGrantedPaths(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	if err := os.MkdirAll(protectedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	granted := filepath.Join(stateRoot, "granted")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{FileRoot: protectedRoot, Workspace: store})
	handler := app.Handler()
	if _, err := app.upsertHostGrant("owner", granted, "read_write"); err != nil {
		t.Fatal(err)
	}

	managedResponse := httptest.NewRecorder()
	handler.ServeHTTP(managedResponse, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "managed", "name": "Managed",
	}))))
	if managedResponse.Code != http.StatusOK {
		t.Fatalf("managed create status=%d body=%s", managedResponse.Code, managedResponse.Body.String())
	}
	var managedPayload struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	}
	if err := json.NewDecoder(managedResponse.Body).Decode(&managedPayload); err != nil {
		t.Fatal(err)
	}
	if managedPayload.Project.Path == "" || !strings.Contains(managedPayload.Project.Path, "-workspaces") {
		t.Fatalf("managed path=%q", managedPayload.Project.Path)
	}

	grantedResponse := httptest.NewRecorder()
	handler.ServeHTTP(grantedResponse, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "granted", "name": "Granted", "path": granted,
	}))))
	if grantedResponse.Code != http.StatusOK {
		t.Fatalf("granted create status=%d body=%s", grantedResponse.Code, grantedResponse.Body.String())
	}
}

func TestProjectUpdateCannotExpandHostAuthority(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	if err := os.MkdirAll(protectedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{FileRoot: protectedRoot, Workspace: store})
	handler := app.Handler()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "owned", "name": "Owned",
	}))))
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}

	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, localWorkspaceRequest(http.MethodPatch, "/api/go/projects/owned", bytes.NewReader(projectPathTestJSON(map[string]any{
		"path": foreign,
	}))))
	if patch.Code != http.StatusForbidden {
		t.Fatalf("patch status=%d body=%s", patch.Code, patch.Body.String())
	}
}

func TestWebFSIgnoresUnauthorizedOwnedProjectPaths(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	ownedRoot := filepath.Join(stateRoot, "owned")
	foreignRoot := filepath.Join(stateRoot, "foreign")
	for _, path := range []string{protectedRoot, ownedRoot, foreignRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(foreignRoot, "secret.txt"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "owned", UserID: "owner", Name: "Owned", Path: ownedRoot,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "foreign", UserID: "other", Name: "Foreign", Path: foreignRoot,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		FileRoot: protectedRoot, Workspace: store,
		SynonLinkAuth: SynonLinkAuthOptions{Username: "operator", Password: "strong-password", UserID: "owner"},
	})
	handler := http.HandlerFunc(srv.handleWebFS)

	workspaceDenied := webFSTestRequest(t, handler, http.MethodPost, "/api/fs/read", "owner", map[string]any{
		"path": "secret.txt", "workspace": ownedRoot,
	})
	requireWebFSStatus(t, workspaceDenied, http.StatusForbidden)

	foreignDenied := webFSTestRequest(t, handler, http.MethodPost, "/api/fs/read", "owner", map[string]any{
		"path": filepath.Join(foreignRoot, "secret.txt"), "workspace": foreignRoot,
	})
	requireWebFSStatus(t, foreignDenied, http.StatusForbidden)
}

func TestWebFSUsesGrantedProjectPath(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	ownedRoot := filepath.Join(stateRoot, "owned")
	if err := os.MkdirAll(ownedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownedRoot, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "owned", UserID: "owner", Name: "Owned", Path: ownedRoot,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{FileRoot: protectedRoot, Workspace: store})
	if _, err := srv.upsertHostGrant("owner", ownedRoot, "read_write"); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(srv.handleWebFS)
	read := webFSTestRequest(t, handler, http.MethodPost, "/api/fs/read", "owner", map[string]any{
		"path": "note.txt", "workspace": ownedRoot,
	})
	requireWebFSStatus(t, read, http.StatusOK)
}

func projectPathTestJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
