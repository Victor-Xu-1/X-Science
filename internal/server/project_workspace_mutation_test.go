package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestProjectCreateMaterializationFailureDoesNotCommit(t *testing.T) {
	app, handler, protectedRoot := newProjectPathAuthTestApp(t)
	if err := os.WriteFile(protectedRoot+"-workspaces", []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "create-failure", "name": "Create failure",
	}))))
	if response.Code == http.StatusOK {
		t.Fatalf("materialization failure accepted: %s", response.Body.String())
	}
	if _, found, err := app.workspaceStore.GetProject("create-failure"); err != nil || found {
		t.Fatalf("failed create committed project: found=%t err=%v", found, err)
	}
	if after, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || after != before {
		t.Fatalf("failed create emitted outbox: before=%d after=%d err=%v", before, after, err)
	}
}

func TestProjectResetMaterializationFailurePreservesProject(t *testing.T) {
	app, handler, protectedRoot := newProjectPathAuthTestApp(t)
	granted := filepath.Join(t.TempDir(), "granted")
	if err := os.Mkdir(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.upsertHostGrant("local", granted, "read_write"); err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "reset-failure", "name": "Before", "path": granted,
	}))))
	if create.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	original, found, err := app.workspaceStore.GetProject("reset-failure")
	if err != nil || !found {
		t.Fatalf("original project: found=%t err=%v", found, err)
	}
	before, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protectedRoot+"-workspaces", []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, localWorkspaceRequest(http.MethodPatch, "/api/go/projects/reset-failure", bytes.NewReader(projectPathTestJSON(map[string]any{
		"path": "", "name": "After",
	}))))
	if patch.Code == http.StatusOK {
		t.Fatalf("materialization failure accepted: %s", patch.Body.String())
	}
	actual, found, err := app.workspaceStore.GetProject("reset-failure")
	if err != nil || !found || actual.Path != original.Path || actual.Name != original.Name || !actual.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("failed reset changed project: original=%#v actual=%#v found=%t err=%v", original, actual, found, err)
	}
	if after, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || after != before {
		t.Fatalf("failed reset emitted outbox: before=%d after=%d err=%v", before, after, err)
	}
}

func TestPreparedProjectCancellationCompensatesNewDirectory(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	path, err := app.defaultAgentKernelWorkspace("cancelled-create")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = app.workspaceStore.CreateProjectRealtimeWithPreparation(ctx, workspace.CreateProjectInput{
		ID: "cancelled-create", UserID: "local", Name: "Cancelled", Path: path,
	}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		resource, err := prepareManagedProjectWorkspace(project.Path)
		cancel()
		return resource, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation error=%v", err)
	}
	if _, found, err := app.workspaceStore.GetProject("cancelled-create"); err != nil || found {
		t.Fatalf("cancelled mutation committed: found=%t err=%v", found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled mutation leaked owned project directory: %v", err)
	}
	if pending, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || pending != 0 {
		t.Fatalf("cancelled mutation emitted outbox: pending=%d err=%v", pending, err)
	}
}

func TestPreparedProjectCancellationPreservesExistingDirectory(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	path, err := app.defaultAgentKernelWorkspace("existing-create")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.materializeManagedProjectWorkspace(path); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(path, "evidence.txt")
	if err := os.WriteFile(evidence, []byte("original evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = app.workspaceStore.CreateProjectRealtimeWithPreparation(ctx, workspace.CreateProjectInput{
		ID: "existing-create", UserID: "local", Name: "Existing", Path: path,
	}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		resource, err := prepareManagedProjectWorkspace(project.Path, managedProjectPreparationOptions{AllowExisting: true})
		cancel()
		return resource, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation error=%v", err)
	}
	if actual, err := os.ReadFile(evidence); err != nil || string(actual) != "original evidence" {
		t.Fatalf("existing evidence changed: %q err=%v", actual, err)
	}
}

func TestPreparedProjectCancellationPreservesNewContents(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	path, err := app.defaultAgentKernelWorkspace("populated-create")
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(path, "evidence.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = app.workspaceStore.CreateProjectRealtimeWithPreparation(ctx, workspace.CreateProjectInput{
		ID: "populated-create", UserID: "local", Name: "Populated", Path: path,
	}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		resource, err := prepareManagedProjectWorkspace(project.Path)
		if err == nil {
			err = os.WriteFile(evidence, []byte("concurrent evidence"), 0o600)
		}
		cancel()
		return resource, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation error=%v", err)
	}
	if actual, readErr := os.ReadFile(evidence); readErr != nil || string(actual) != "concurrent evidence" {
		t.Fatalf("new evidence discarded: %q err=%v", actual, readErr)
	}
}

func TestProjectCreateRefusesSymbolicManagedWorkspace(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	path, err := app.defaultAgentKernelWorkspace("symbolic-create")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "symbolic-create", "name": "Symbolic",
	}))))
	if response.Code == http.StatusOK {
		t.Fatalf("symbolic managed authority accepted: %s", response.Body.String())
	}
	if _, found, err := app.workspaceStore.GetProject("symbolic-create"); err != nil || found {
		t.Fatalf("symbolic managed project committed: found=%t err=%v", found, err)
	}
}

func TestPreparedProjectResetCancellationPreservesPriorPath(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	original, err := app.workspaceStore.CreateProject(workspace.CreateProjectInput{
		ID: "cancelled-reset", UserID: "local", Name: "Before", Path: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := app.defaultAgentKernelWorkspace(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := "After"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = app.workspaceStore.UpdateProjectRealtimeWithPreparation(ctx, original.ID, "local", workspace.UpdateProjectInput{
		Name: &name, Path: &path,
	}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		resource, err := prepareManagedProjectWorkspace(project.Path)
		cancel()
		return resource, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reset error=%v", err)
	}
	actual, found, err := app.workspaceStore.GetProject(original.ID)
	if err != nil || !found || actual.Path != original.Path || actual.Name != original.Name || !actual.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("cancelled reset changed project: original=%#v actual=%#v found=%t err=%v", original, actual, found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled reset leaked owned directory: %v", err)
	}
	if pending, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || pending != 0 {
		t.Fatalf("cancelled reset emitted outbox: pending=%d err=%v", pending, err)
	}
}

func TestPreparedProjectCompensationRetainsChangedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent", "project")
	resource, err := prepareManagedProjectWorkspace(path)
	if err != nil || resource.Rollback == nil {
		t.Fatalf("prepare: rollback=%t err=%v", resource.Rollback != nil, err)
	}
	defer resource.Release()
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := resource.Rollback(); err == nil {
		t.Fatal("replacement directory removed without matching inode")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("replacement directory lost: %v", err)
	}
}
