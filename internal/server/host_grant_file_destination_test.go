package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostGrantFileSnapshotExternalDestinationNeedsCurrentWritableAuthority(t *testing.T) {
	for _, change := range []string{"read-only-destination", "retargeted-destination", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			app, frame, sourceRoot := hostFileReferenceFixture(t, "read")
			if err := os.WriteFile(filepath.Join(sourceRoot, "source.txt"), []byte("snapshot"), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "workspace")
			if err := os.MkdirAll(filepath.Join(destination, "inputs"), 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := app.upsertHostGrant("local", destination, "read_write"); err != nil {
				t.Fatal(err)
			}
			response := p3JSONRequest(t, app, http.MethodPatch, "/api/go/projects/"+frame.ProjectID, map[string]any{"path": destination}, "local")
			if response.Code != http.StatusOK {
				t.Fatalf("prepare owned external workspace: %d %s", response.Code, response.Body.String())
			}
			directory, err := os.OpenRoot(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			inputs, err := directory.OpenRoot("inputs")
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.Close()
			checkDestination, err := app.hostFileDestinationCheck("local", frame.ProjectID, destination, directory, inputs)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "read-only-destination":
				if _, err := app.upsertHostGrant("local", destination, "read"); err != nil {
					t.Fatal(err)
				}
			case "retargeted-destination":
				if err := os.Rename(destination, destination+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(destination, "inputs"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			// Capture the source after the destination change: its valid grant and
			// current epoch must not substitute for destination write authority.
			snapshot, err := app.openHostGrantFileSnapshot(context.Background(), "local", hostGrantFileReference{grantID: sourceRoot, path: "source.txt"})
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.close()
			_, _, err = copyWebConversationInputFileWithCommit(context.Background(), snapshot.file, "source.txt", inputs, 1024, func(action func() error) error {
				return snapshot.commit(context.Background(), func() error {
					if err := checkDestination(); err != nil {
						return err
					}
					return action()
				})
			})
			if change == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(filepath.Join(destination, "inputs", "source.txt")); err != nil || string(data) != "snapshot" {
					t.Fatalf("external snapshot=%q %v", data, err)
				}
			} else if err == nil {
				t.Fatal("source grant authorized an invalid external destination")
			}
			project, found, err := app.workspaceStore.GetProject(frame.ProjectID)
			if err != nil || !found || project.Path != destination {
				t.Fatalf("reference changed project cwd: %#v %v", project, err)
			}
		})
	}
}

func TestWebConversationHostGrantFileReferenceKeepsTotalInputBudget(t *testing.T) {
	app, frame, root := hostFileReferenceFixture(t, "read")
	files := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		name := fmt.Sprintf("bounded-%d.bin", index)
		file, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		size := maxWebConversationInputFileBytes
		if index == 4 {
			size = 1
		}
		if err := file.Truncate(size); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		files = append(files, hostFileReferenceURI(root, name))
	}
	request := newLoopbackTestRequest(http.MethodPost, "/", nil)
	if result, err := app.materializeWebConversationInputFiles(context.Background(), request, frame, files); err == nil {
		t.Fatalf("256 MiB total input budget bypassed: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(hostFileTaskRoot(t, app, frame), "inputs", "bounded-4.bin")); !os.IsNotExist(err) {
		t.Fatalf("over-budget fifth file was published: %v", err)
	}
}

func TestWebConversationHostGrantFileReferenceAllowsConfinedSymlink(t *testing.T) {
	app, frame, root := hostFileReferenceFixture(t, "read")
	if err := os.WriteFile(filepath.Join(root, "original.txt"), []byte("inside root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/", nil)
	files, err := app.materializeWebConversationInputFiles(context.Background(), request, frame, []string{hostFileReferenceURI(root, "alias.txt")})
	if err != nil || len(files) != 1 || files[0] != "inputs/alias.txt" {
		t.Fatalf("confined symlink snapshot=%#v %v", files, err)
	}
	if data, err := os.ReadFile(filepath.Join(hostFileTaskRoot(t, app, frame), "inputs", "alias.txt")); err != nil || string(data) != "inside root" {
		t.Fatalf("confined symlink bytes=%q %v", data, err)
	}
}

func hostFileExternalDestination(t *testing.T, app *Server, projectID string, withInputs bool) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if withInputs {
		if err := os.Mkdir(filepath.Join(destination, "inputs"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.upsertHostGrant("local", destination, "read_write"); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, app, http.MethodPatch, "/api/go/projects/"+projectID, map[string]any{"path": destination}, "local")
	if response.Code != http.StatusOK {
		t.Fatalf("prepare owned external workspace: %d %s", response.Code, response.Body.String())
	}
	return destination
}

func TestHostGrantFileSnapshotRejectsNamespaceAfterDestinationGrantChange(t *testing.T) {
	for _, change := range []string{"downgrade", "revoke", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			app, frame, _ := hostFileReferenceFixture(t, "read")
			destination := hostFileExternalDestination(t, app, frame.ProjectID, false)
			access, found, err := app.workspaceStore.GetKernelFrameAccessContext(context.Background(), frame.ID)
			if err != nil || !found {
				t.Fatalf("frame access found=%v err=%v", found, err)
			}
			selected, managed, err := app.selectAgentWorkspace(access, access.ProjectPath)
			if err != nil || managed || selected != destination {
				t.Fatalf("select writable external workspace=%q managed=%v err=%v", selected, managed, err)
			}
			// The real grant changes after selection but before the same directory
			// preparation used by materializeWebConversationInputFiles.
			switch change {
			case "downgrade":
				if _, err := app.upsertHostGrant("local", destination, "read"); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				if removed, err := app.revokeHostGrant("local", destination); err != nil || !removed {
					t.Fatalf("revoke=%v err=%v", removed, err)
				}
			}
			prepared, directory, inputs, _, prepareErr := app.prepareWebConversationInputDirectory(access.UserID, access.Frame.ProjectID, selected, managed)
			if directory != nil {
				defer directory.Close()
			}
			if inputs != nil {
				defer inputs.Close()
			}
			if change == "unchanged" {
				if prepareErr != nil || prepared != destination || directory == nil || inputs == nil {
					t.Fatalf("prepare authorized inputs=%q directory=%v inputs=%v err=%v", prepared, directory != nil, inputs != nil, prepareErr)
				}
				if entries, err := os.ReadDir(filepath.Join(destination, "inputs")); err != nil || len(entries) != 0 {
					t.Fatalf("authorized empty inputs=%#v err=%v", entries, err)
				}
			} else {
				if prepareErr == nil {
					t.Error("destination preparation accepted a grant changed after selection")
				}
				if _, err := os.Lstat(filepath.Join(destination, "inputs")); !os.IsNotExist(err) {
					t.Errorf("unauthorized destination inputs namespace was created: %v", err)
				}
			}
			project, found, err := app.workspaceStore.GetProject(frame.ProjectID)
			if err != nil || !found || project.Path != destination {
				t.Fatalf("preparation changed project cwd: %#v err=%v", project, err)
			}
		})
	}
}

func TestHostGrantFileSnapshotRejectsStageOpenAfterDestinationRevoked(t *testing.T) {
	for _, entry := range []string{"guarded-stage-control", "copy-preflight", "copy-authorized-control"} {
		t.Run(entry, func(t *testing.T) {
			app, frame, sourceRoot := hostFileReferenceFixture(t, "read")
			const content = "actual source bytes must not be read before admission"
			if err := os.WriteFile(filepath.Join(sourceRoot, "source.txt"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := hostFileExternalDestination(t, app, frame.ProjectID, true)
			directory, err := os.OpenRoot(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			inputs, err := directory.OpenRoot("inputs")
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.Close()
			checkDestination, err := app.hostFileDestinationCheck("local", frame.ProjectID, destination, directory, inputs)
			if err != nil {
				t.Fatal(err)
			}
			revoked := entry != "copy-authorized-control"
			if revoked {
				if removed, err := app.revokeHostGrant("local", destination); err != nil || !removed {
					t.Fatalf("revoke=%v err=%v", removed, err)
				}
			}
			// Capture the source after the destination mutation so the valid source
			// epoch cannot masquerade as destination write authorization.
			snapshot, err := app.openHostGrantFileSnapshot(context.Background(), "local", hostGrantFileReference{grantID: sourceRoot, path: "source.txt"})
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.close()
			guardCalls, sawIncomingAtGuard := 0, false
			guard := func(action func() error) error {
				guardCalls++
				entries, err := os.ReadDir(filepath.Join(destination, "inputs"))
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range entries {
					if strings.HasPrefix(item.Name(), ".x-science-incoming-") {
						sawIncomingAtGuard = true
					}
				}
				return snapshot.commit(context.Background(), func() error {
					if err := checkDestination(); err != nil {
						return err
					}
					return action()
				})
			}
			if entry == "guarded-stage-control" {
				stage, stageErr := stageWorkspaceFileWithCommit(context.Background(), inputs, snapshot.file, 1024, guard)
				if stage != nil {
					defer stage.close()
					t.Error("denied stage returned a temporary file")
				}
				err = stageErr
			} else {
				_, _, err = copyWebConversationInputFileWithCommit(context.Background(), snapshot.file, "source.txt", inputs, 1024, guard)
			}
			offset, seekErr := snapshot.file.Seek(0, io.SeekCurrent)
			if seekErr != nil {
				t.Fatal(seekErr)
			}
			if guardCalls == 0 {
				t.Error("copy bypassed the source/destination guard")
			}
			if revoked {
				if err == nil {
					t.Error("revoked destination accepted staging")
				}
				if offset != 0 {
					t.Errorf("source was read before destination admission: offset=%d", offset)
				}
				if sawIncomingAtGuard {
					t.Error("temporary namespace was created before destination admission")
				}
				if entries, err := os.ReadDir(filepath.Join(destination, "inputs")); err != nil || len(entries) != 0 {
					t.Fatalf("denied copy left output: %#v err=%v", entries, err)
				}
			} else {
				if err != nil || offset != int64(len(content)) {
					t.Fatalf("authorized copy offset=%d err=%v", offset, err)
				}
				if got, err := os.ReadFile(filepath.Join(destination, "inputs", "source.txt")); err != nil || string(got) != content {
					t.Fatalf("authorized snapshot=%q err=%v", got, err)
				}
			}
		})
	}
}
