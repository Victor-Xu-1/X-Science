package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestManagedProjectSymlinkDoesNotExpandAdmission(t *testing.T) {
	for _, parentAlias := range []bool{false, true} {
		name := "project"
		if parentAlias {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			fixture, _, stateRoot := newProjectPathAuthTestApp(t)
			app := New(Options{FileRoot: stateRoot, Workspace: fixture.workspaceStore,
				XScienceLinkAuth: XScienceLinkAuthOptions{Username: "operator", Password: "test-password", UserID: "local"},
			})
			const projectID = "retained-managed"
			managed, err := app.defaultAgentKernelWorkspace(projectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(managed, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := app.workspaceStore.CreateProject(workspace.CreateProjectInput{
				ID: projectID, UserID: "local", Name: "Retained", Path: managed,
			}); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			target := outside
			link := managed
			if parentAlias {
				link = filepath.Dir(managed)
				target = filepath.Join(outside, filepath.Base(managed))
			}
			secret := filepath.Join(target, "secret.txt")
			writeWebFSTestFile(t, secret, "private")
			if err := os.Rename(link, link+"-retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			response := webFSTestRequest(t, http.HandlerFunc(app.handleWebFS), http.MethodPost, "/api/fs/read", "local", map[string]any{"path": secret})
			if response.Code != http.StatusForbidden {
				t.Errorf("symbolic managed Web-FS read status=%d body=%s", response.Code, response.Body.String())
			}
			if path, admitted := app.liveWritableProjectWorkspace("local", projectID, managed); admitted {
				t.Errorf("symbolic managed kernel root admitted: %q", path)
			}
			if path, err := app.materializeAdmittedManagedWorkspace(projectID, filepath.Join(managed, "tasks", "probe")); err == nil {
				t.Errorf("symbolic managed task materialized: %q", path)
			}
			if _, err := os.Stat(filepath.Join(target, "tasks", "probe")); !os.IsNotExist(err) {
				t.Errorf("foreign task directory touched: %v", err)
			}
			if raw, err := os.ReadFile(secret); err != nil || string(raw) != "private" {
				t.Fatalf("foreign evidence changed: %q err=%v", raw, err)
			}
		})
	}
}

func TestPreparedProjectOpenFailureUsesGuardedCompensation(t *testing.T) {
	for _, scenario := range []string{"fresh", "existing", "populated", "replacement", "preflight", "unproven_pin"} {
		t.Run(scenario, func(t *testing.T) {
			app, _, _ := newProjectPathAuthTestApp(t)
			path, err := app.defaultAgentKernelWorkspace("open-failure")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "existing" {
				writeWebFSTestFile(t, filepath.Join(path, "keep.txt"), "existing")
			}
			injected := errors.New("forced child-open failure")
			ops := managedProjectPreparationOps{
				openPin: func(root *os.Root, name string) (*os.File, error) {
					if scenario == "unproven_pin" && name != "." {
						return nil, injected
					}
					return root.Open(name)
				},
				openChild: func(root *os.Root, name string) (*os.Root, error) {
					if name == "." {
						if scenario == "preflight" {
							return nil, injected
						}
						return root.OpenRoot(name)
					}
					if scenario == "preflight" || scenario == "unproven_pin" {
						return root.OpenRoot(name)
					}
					if scenario == "populated" {
						writeWebFSTestFile(t, filepath.Join(path, "keep.txt"), "new content")
					}
					if scenario == "replacement" {
						if err := os.Rename(path, path+"-retained"); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					return nil, injected
				},
			}
			_, err = app.workspaceStore.CreateProjectRealtimeWithPreparation(context.Background(), workspace.CreateProjectInput{
				ID: "open-failure", UserID: "local", Name: "Open failure", Path: path,
			}, "", func(project workspace.Project) (workspace.PreparedProjectResource, error) {
				return prepareManagedProjectWorkspaceWithOps(project.Path, managedProjectPreparationOptions{AllowExisting: true}, ops)
			})
			if !errors.Is(err, injected) {
				t.Fatalf("failed preparation error=%v", err)
			}
			if _, found, err := app.workspaceStore.GetProject("open-failure"); err != nil || found {
				t.Fatalf("failed preparation committed: found=%t err=%v", found, err)
			}
			if pending, err := app.workspaceStore.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || pending != 0 {
				t.Fatalf("failed preparation outbox=%d err=%v", pending, err)
			}
			if scenario == "fresh" || scenario == "preflight" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("failed preparation leaked directory: %v", err)
				}
			} else if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Fatalf("retained directory lost: %v", err)
			}
			if scenario == "existing" || scenario == "populated" {
				if _, err := os.ReadFile(filepath.Join(path, "keep.txt")); err != nil {
					t.Fatalf("user content lost: %v", err)
				}
			}
			if scenario == "unproven_pin" && !strings.Contains(err.Error(), "identity") {
				t.Fatalf("unknown identity not diagnosed: %v", err)
			}
		})
	}
}

func TestNewManagedCreateRefusesRetainedDirectory(t *testing.T) {
	app, handler, _ := newProjectPathAuthTestApp(t)
	path, err := app.defaultAgentKernelWorkspace("reused-id")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "retained.txt")
	writeWebFSTestFile(t, marker, "retained owner data")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, localWorkspaceRequest(http.MethodPost, "/api/go/projects", bytes.NewReader(projectPathTestJSON(map[string]any{
		"id": "reused-id", "name": "New owner",
	}))))
	if response.Code == http.StatusOK {
		t.Fatalf("retained managed directory admitted: %s", response.Body.String())
	}
	if _, found, err := app.workspaceStore.GetProject("reused-id"); err != nil || found {
		t.Fatalf("reused managed directory committed: found=%t err=%v", found, err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retained owner data" {
		t.Fatalf("retained data changed: %q err=%v", raw, err)
	}
}
