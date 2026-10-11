package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestManagedWorkspaceMutationRejectsCallerPathEscape(t *testing.T) {
	app, store := newManagedWorkspaceMutationTestApp(t)
	const projectID = "escape-project"
	managed, err := app.managedProjectWorkspacePath(projectID)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	escapes := []string{
		outside,
		filepath.Join(managed, "..", "..", filepath.Base(outside)),
		filepath.Join(app.fileRoot, "secret-escape"),
		filepath.Join(managed, "..", "foreign-"+filepath.Base(managed)),
	}
	for _, path := range escapes {
		if _, err := app.prepareManagedProjectWorkspaceForMutation(context.Background(), store, projectID, path); err == nil {
			t.Fatalf("escaped path was accepted: %q", path)
		}
		if _, err := os.Lstat(path); err == nil && path != outside {
			t.Fatalf("escape created or touched %q", path)
		}
	}
	if _, err := os.Lstat(managed); !os.IsNotExist(err) {
		t.Fatalf("rejected escape still materialized managed workspace: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil {
		t.Fatalf("ungranted host path was disturbed: %v", err)
	}
}

func TestManagedWorkspaceMutationMaterializesBeforeCommit(t *testing.T) {
	app, store := newManagedWorkspaceMutationTestApp(t)
	const projectID = "materialize-project"
	managed, err := app.managedProjectWorkspacePath(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed workspace existed before prepare: %v", err)
	}
	prep, err := app.prepareManagedProjectWorkspaceForMutation(context.Background(), store, projectID, managed)
	if err != nil {
		t.Fatal(err)
	}
	if prep == nil || !prep.createdFresh || prep.path != managed {
		t.Fatalf("prep=%#v managed=%q", prep, managed)
	}
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		t.Fatalf("managed workspace missing before persist commit: %v", err)
	}
	if refs, err := store.CountProjectPathDurableReferences(context.Background(), managed, ""); err != nil || refs != 0 {
		t.Fatalf("durable refs before persist refs=%d err=%v", refs, err)
	}
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: projectID, UserID: "local", Name: "Materialized", Path: managed,
	}); err != nil {
		t.Fatal(err)
	}
	prep.Commit()
	if err := prep.Compensate(context.Background(), store, projectID); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		t.Fatalf("committed workspace was removed: %v", err)
	}
}

func TestManagedWorkspaceCompensationRemovesOnlyEmptyFreshDirs(t *testing.T) {
	app, store := newManagedWorkspaceMutationTestApp(t)
	freshManaged := prepareManagedWorkspace(t, app, store, "fresh-project")
	if err := freshManaged.Compensate(context.Background(), store, "fresh-project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(freshManaged.path); !os.IsNotExist(err) {
		t.Fatalf("empty fresh workspace was not compensated: %v", err)
	}

	existingManaged, err := app.managedProjectWorkspacePath("existing-project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(existingManaged, 0o700); err != nil {
		t.Fatal(err)
	}
	existingPrep, err := app.prepareManagedProjectWorkspaceForMutation(context.Background(), store, "existing-project", existingManaged)
	if err != nil {
		t.Fatal(err)
	}
	if existingPrep.createdFresh {
		t.Fatal("pre-existing workspace was treated as created by this request")
	}
	if err := existingPrep.Compensate(context.Background(), store, "existing-project"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(existingManaged); err != nil || !info.IsDir() {
		t.Fatalf("pre-existing workspace was removed: %v", err)
	}

	populated := prepareManagedWorkspace(t, app, store, "populated-project")
	if err := os.WriteFile(filepath.Join(populated.path, "keep.txt"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := populated.Compensate(context.Background(), store, "populated-project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(populated.path, "keep.txt")); err != nil {
		t.Fatalf("non-empty workspace created by this request was removed: %v", err)
	}

	referenced := prepareManagedWorkspace(t, app, store, "referenced-project")
	if _, err := store.CreateProject(workspace.CreateProjectInput{
		ID: "other-owner", UserID: "local", Name: "Other", Path: referenced.path,
	}); err != nil {
		t.Fatal(err)
	}
	if err := referenced.Compensate(context.Background(), store, "referenced-project"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(referenced.path); err != nil || !info.IsDir() {
		t.Fatalf("workspace with durable refs was removed: %v", err)
	}

	granted := filepath.Join(t.TempDir(), "granted-host")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := app.upsertHostGrant("local", granted, "read_write"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.prepareManagedProjectWorkspaceForMutation(context.Background(), store, "granted-project", granted); err == nil {
		t.Fatal("host-granted path was treated as a managed workspace mutation")
	}
	if info, err := os.Stat(granted); err != nil || !info.IsDir() {
		t.Fatalf("granted host path was disturbed: %v", err)
	}
}

func newManagedWorkspaceMutationTestApp(t *testing.T) (*Server, *workspace.Store) {
	t.Helper()
	app, _, _ := newProjectPathAuthTestApp(t)
	return app, app.workspaceStore
}

func prepareManagedWorkspace(t *testing.T, app *Server, store *workspace.Store, projectID string) *managedProjectWorkspacePreparation {
	t.Helper()
	managed, err := app.managedProjectWorkspacePath(projectID)
	if err != nil {
		t.Fatal(err)
	}
	prep, err := app.prepareManagedProjectWorkspaceForMutation(context.Background(), store, projectID, managed+"/"+filepath.Join("..", filepath.Base(managed)))
	if err != nil {
		t.Fatal(err)
	}
	if prep.path != managed {
		t.Fatalf("caller-supplied expression was retained: got %q want %q", prep.path, managed)
	}
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		t.Fatalf("managed workspace missing after prepare: %v", err)
	}
	return prep
}
