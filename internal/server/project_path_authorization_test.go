package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
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
	if _, err := app.upsertHostGrant("local", granted, "read_write"); err != nil {
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
		XScienceLinkAuth: XScienceLinkAuthOptions{Username: "operator", Password: "strong-password", UserID: "owner"},
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

func TestProjectCreateRejectsProtectedAndForeignManagedPaths(t *testing.T) {
	app, handler, protectedRoot := newProjectPathAuthTestApp(t)
	foreignManaged := httptest.NewRecorder()
	handler.ServeHTTP(foreignManaged, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "managed-owner", "name": "Managed owner",
	}))))
	if foreignManaged.Code != http.StatusOK {
		t.Fatalf("managed create status=%d body=%s", foreignManaged.Code, foreignManaged.Body.String())
	}
	var managedPayload struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	}
	if err := json.NewDecoder(foreignManaged.Body).Decode(&managedPayload); err != nil {
		t.Fatal(err)
	}
	if managedPayload.Project.Path == "" {
		t.Fatal("managed path is empty")
	}

	protected := httptest.NewRecorder()
	handler.ServeHTTP(protected, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "protected", "name": "Protected", "path": protectedRoot,
	}))))
	if protected.Code != http.StatusBadRequest {
		t.Fatalf("protected create status=%d body=%s", protected.Code, protected.Body.String())
	}

	stolen := httptest.NewRecorder()
	handler.ServeHTTP(stolen, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "stolen", "name": "Stolen", "path": managedPayload.Project.Path,
	}))))
	if stolen.Code != http.StatusBadRequest {
		t.Fatalf("foreign managed create status=%d body=%s", stolen.Code, stolen.Body.String())
	}
	_ = app
}

func TestProjectCreateRejectsReadOnlyGrant(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	readOnly := filepath.Join(t.TempDir(), "read-only")
	if err := os.MkdirAll(readOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.upsertHostGrant("local", readOnly, "read"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "readonly", "name": "Read only", "path": readOnly,
	}))))
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only create status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWebFSHonorsReadGrantOnOwnedProjectPath(t *testing.T) {
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
	if _, err := srv.upsertHostGrant("owner", ownedRoot, "read"); err != nil {
		t.Fatal(err)
	}
	root, ok := srv.authorizedProjectWebFSRoot("owner", "owned", ownedRoot)
	if !ok || root.Writable {
		t.Fatalf("read grant root=%#v ok=%t", root, ok)
	}
	handler := http.HandlerFunc(srv.handleWebFS)
	read := webFSTestRequest(t, handler, http.MethodPost, "/api/fs/read", "owner", map[string]any{
		"path": "note.txt", "workspace": ownedRoot,
	})
	requireWebFSStatus(t, read, http.StatusOK)
	write := webFSTestRequest(t, handler, http.MethodPost, "/api/fs/write", "owner", map[string]any{
		"path": "note.txt", "workspace": ownedRoot, "data": "nope",
	})
	requireWebFSStatus(t, write, http.StatusForbidden)
}

func TestWebFSPrefersWriteGrantOverParentReadGrant(t *testing.T) {
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	parent := filepath.Join(stateRoot, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "owned", UserID: "owner", Name: "Owned", Path: child,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{FileRoot: protectedRoot, Workspace: store})
	if _, err := srv.upsertHostGrant("owner", parent, "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.upsertHostGrant("owner", child, "read_write"); err != nil {
		t.Fatal(err)
	}
	root, ok := srv.authorizedProjectWebFSRoot("owner", "owned", child)
	if !ok || !root.Writable {
		t.Fatalf("overlapping grant root=%#v ok=%t", root, ok)
	}
}

func TestProjectPatchEmptyPathResetsToManagedWorkspace(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	granted := filepath.Join(t.TempDir(), "granted")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.upsertHostGrant("local", granted, "read_write"); err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "reset", "name": "Reset", "path": granted,
	}))))
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, localWorkspaceRequest(http.MethodPatch, "/api/go/projects/reset", bytes.NewReader(projectPathTestJSON(map[string]any{
		"path": "",
	}))))
	if patch.Code != http.StatusOK {
		t.Fatalf("empty patch status=%d body=%s", patch.Code, patch.Body.String())
	}
	var payload struct {
		Project struct {
			Path string `json:"path"`
		} `json:"project"`
	}
	if err := json.NewDecoder(patch.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project.Path == "" || payload.Project.Path == granted || !strings.Contains(payload.Project.Path, "-workspaces") {
		t.Fatalf("reset path=%q granted=%q", payload.Project.Path, granted)
	}
	if info, err := os.Stat(payload.Project.Path); err != nil || !info.IsDir() {
		t.Fatalf("managed workspace missing: %v", err)
	}
}

func TestProjectCreateDoesNotLeakManagedDirectoryOnFailedPersist(t *testing.T) {
	_, handler, protectedRoot := newProjectPathAuthTestApp(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"name": "",
	}))))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid create status=%d body=%s", response.Code, response.Body.String())
	}
	workspacesRoot := protectedRoot + "-workspaces"
	if info, err := os.Stat(workspacesRoot); err == nil && info.IsDir() {
		entries, readErr := os.ReadDir(workspacesRoot)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Fatalf("managed inode leak: %v", namesOf(entries))
		}
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestRevokedGrantDoesNotRemainWritableKernelWorkspace(t *testing.T) {
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
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "granted-project", UserID: "local", Name: "Granted", Path: granted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{
		ID: "granted-frame", ProjectID: "granted-project", AgentName: "OPERON", Status: "processing", ConversationType: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	access, found, err := store.GetKernelFrameAccessContext(context.Background(), "granted-frame")
	if err != nil || !found {
		t.Fatalf("kernel access found=%t err=%v", found, err)
	}
	if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:granted-frame", OwnerID: access.UserID, ExternalID: access.Frame.ID, SessionID: access.Frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: access.Frame.ProjectID,
		RootFrameID: access.Frame.RootFrameID, FrameID: access.Frame.ID, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{FileRoot: protectedRoot, Workspace: store, Transcript: repo})
	if _, err := app.upsertHostGrant("local", granted, "read_write"); err != nil {
		t.Fatal(err)
	}
	_, workspaceDir, authorized := app.resolveAgentWorkspaceAuthority(context.Background(), "granted-frame")
	if !authorized || !hostPathWithin(granted, workspaceDir) {
		t.Fatalf("granted workspace=%q authorized=%t", workspaceDir, authorized)
	}

	if _, err := app.revokeHostGrant("local", granted); err != nil {
		t.Fatal(err)
	}
	_, afterRevoke, authorized := app.resolveAgentWorkspaceAuthority(context.Background(), "granted-frame")
	if !authorized {
		t.Fatal("revoked grant must still admit a managed task workspace")
	}
	if hostPathWithin(granted, afterRevoke) {
		t.Fatalf("revoked host path remained kernel workspace: %q", afterRevoke)
	}
	if !strings.Contains(afterRevoke, "-workspaces") {
		t.Fatalf("fallback workspace=%q", afterRevoke)
	}
	if info, err := os.Stat(afterRevoke); err != nil || !info.IsDir() {
		t.Fatalf("fallback workspace missing: %v", err)
	}

	if _, err := app.upsertHostGrant("local", granted, "read"); err != nil {
		t.Fatal(err)
	}
	_, afterRead, authorized := app.resolveAgentWorkspaceAuthority(context.Background(), "granted-frame")
	if !authorized || hostPathWithin(granted, afterRead) {
		t.Fatalf("read-only grant kept writable kernel workspace=%q authorized=%t", afterRead, authorized)
	}
}

func newProjectPathAuthTestApp(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	stateRoot := t.TempDir()
	protectedRoot := filepath.Join(stateRoot, "runtime")
	if err := os.MkdirAll(protectedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := workspace.Open(filepath.Join(stateRoot, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(Options{FileRoot: protectedRoot, Workspace: store})
	return app, app.Handler(), protectedRoot
}

func namesOf(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func projectPathTestJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
