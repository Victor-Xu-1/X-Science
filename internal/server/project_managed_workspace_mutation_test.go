package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workspace "synon-go/internal/persistence/workspace"
)

func TestManagedWorkspaceMutationRejectsCallerPathEscape(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	const projectID = "escape-project"
	managed, err := app.defaultAgentKernelWorkspace(projectID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := app.defaultAgentKernelWorkspace("foreign-project")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	for _, scenario := range []struct{ name, path string }{
		{"outside", filepath.Join(outside, "ungranted")},
		{"workspaces_root", filepath.Dir(managed)},
		{"foreign_project", foreign},
		{"protected_data", filepath.Join(app.fileRoot, "secret-escape")},
		{"child_mismatch", filepath.Join(managed, "child")},
		{"empty", ""},
		{"relative", filepath.Base(managed)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			resource, err := app.managedProjectPreparation(true)(workspace.Project{ID: projectID, Path: scenario.path})
			if resource.Release != nil {
				defer resource.Release()
			}
			if err == nil {
				t.Fatalf("escaped managed path accepted: %q", scenario.path)
			}
		})
	}
}

func TestManagedWorkspaceMutationRejectsSymbolicAuthority(t *testing.T) {
	for _, symbolicParent := range []bool{false, true} {
		name := "project"
		if symbolicParent {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			app, _, _ := newProjectPathAuthTestApp(t)
			const projectID = "symbolic-project"
			managed, err := app.defaultAgentKernelWorkspace(projectID)
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			evidence := filepath.Join(outside, "evidence.txt")
			writeWebFSTestFile(t, evidence, "keep")
			link := managed
			if symbolicParent {
				link = filepath.Dir(managed)
			} else if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			resource, err := app.managedProjectPreparation(true)(workspace.Project{ID: projectID, Path: managed})
			if resource.Release != nil {
				defer resource.Release()
			}
			if err == nil {
				t.Fatal("symbolic managed authority accepted")
			}
			if raw, err := os.ReadFile(evidence); err != nil || string(raw) != "keep" {
				t.Fatalf("foreign evidence disturbed: %q err=%v", raw, err)
			}
		})
	}
}

func TestManagedWorkspaceMutationUsesSharedPreparation(t *testing.T) {
	app, _, _ := newProjectPathAuthTestApp(t)
	const projectID = "shared-preparation"
	managed, err := app.defaultAgentKernelWorkspace(projectID)
	if err != nil {
		t.Fatal(err)
	}
	prepare := app.managedProjectPreparation(true)
	called := false
	project, err := app.workspaceStore.CreateProjectRealtimeWithPreparation(context.Background(), workspace.CreateProjectInput{
		ID: projectID, UserID: "local", Name: "Prepared", Path: managed,
	}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
		// Normalize a harmless expression to the server root before filesystem I/O.
		project.Path = managed + string(filepath.Separator) + filepath.Join("..", filepath.Base(managed))
		resource, err := prepare(project)
		if err == nil {
			info, statErr := os.Stat(managed)
			if statErr != nil || !info.IsDir() {
				t.Fatalf("managed workspace missing before commit: %v", statErr)
			}
			called = true
		}
		return resource, err
	})
	if err != nil || !called || project.Path != managed {
		t.Fatalf("shared preparation: called=%t project=%#v err=%v", called, project, err)
	}
	if _, found, err := app.workspaceStore.GetProject(projectID); err != nil || !found {
		t.Fatalf("prepared project not committed: found=%t err=%v", found, err)
	}
	if pending, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || pending != 1 {
		t.Fatalf("shared mutation outbox=%d err=%v", pending, err)
	}
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		t.Fatalf("committed workspace missing: %v", err)
	}
}
