package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostGrantFileSnapshotRechecksGrantAndRootAtPublication(t *testing.T) {
	for _, change := range []string{"revoke", "mode", "regrant", "root-retarget", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			app, _, parent := hostFileReferenceFixture(t, "read")
			root := filepath.Join(parent, "exact-grant")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("snapshot"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := app.upsertHostGrant("local", root, "read"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := app.openHostGrantFileSnapshot(context.Background(), "local", hostGrantFileReference{grantID: root, path: "source.txt"})
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.close()
			output := t.TempDir()
			inputs, err := os.OpenRoot(output)
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.Close()
			commits := 0
			_, _, err = copyWebConversationInputFileWithCommit(context.Background(), snapshot.file, "source.txt", inputs, 1024, func(action func() error) error {
				commits++
				if commits == 1 {
					return snapshot.commit(context.Background(), action)
				}
				if commits != 2 {
					t.Fatalf("unexpected publication guard call %d", commits)
				}
				if offset, err := snapshot.file.Seek(0, io.SeekCurrent); err != nil || offset != int64(len("snapshot")) {
					t.Fatalf("publication mutation ran before source copy: offset=%d err=%v", offset, err)
				}
				switch change {
				case "revoke", "regrant":
					if removed, err := app.revokeHostGrant("local", root); err != nil || !removed {
						t.Fatalf("revoke=%v %v", removed, err)
					}
					if change == "regrant" {
						if _, err := app.upsertHostGrant("local", root, "read"); err != nil {
							t.Fatal(err)
						}
					}
				case "mode":
					if _, err := app.upsertHostGrant("local", root, "read_write"); err != nil {
						t.Fatal(err)
					}
				case "root-retarget":
					if err := os.Rename(root, root+"-held"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(root, 0o700); err != nil {
						t.Fatal(err)
					}
				case "unchanged":
					if _, err := app.upsertHostGrant("local", root, "read"); err != nil {
						t.Fatal(err)
					}
				}
				return snapshot.commit(context.Background(), action)
			})
			if commits != 2 {
				t.Fatalf("publication guard was not reached after staging: %d", commits)
			}
			if change == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(filepath.Join(output, "source.txt")); err != nil || string(data) != "snapshot" {
					t.Fatalf("snapshot=%q %v", data, err)
				}
			} else {
				if err == nil {
					t.Fatal("changed grant/root admitted publication")
				}
				if entries, err := os.ReadDir(output); err != nil || len(entries) != 0 {
					t.Fatalf("failed snapshot left output: %#v %v", entries, err)
				}
			}
		})
	}
}

func TestWorkspaceFileGuardedPublicationRejectsChangedPostHashTarget(t *testing.T) {
	for _, change := range []string{"replacement", "same-inode-write", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "existing.txt")
			if err := os.WriteFile(path, []byte("snapshot"), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			stage, err := stageWorkspaceFile(context.Background(), root, strings.NewReader("snapshot"), 1024)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.close()
			commits := 0
			err = stage.publishWithCommit(context.Background(), "existing.txt", func(action func() error) error {
				commits++
				if commits == 2 {
					switch change {
					case "replacement":
						replacement := filepath.Join(directory, "replacement.txt")
						if err := os.WriteFile(replacement, []byte("tampered"), 0o600); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(replacement, path); err != nil {
							t.Fatal(err)
						}
					case "same-inode-write":
						if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(path, before.ModTime(), before.ModTime().Add(time.Second)); err != nil {
							t.Fatal(err)
						}
					}
				}
				return action()
			})
			if commits != 2 {
				t.Fatalf("commit calls=%d; existing-target hash was not outside the final commit", commits)
			}
			want := "snapshot"
			if change != "unchanged" {
				want = "tampered"
				if !errors.Is(err, errWorkspaceFileConflict) {
					t.Fatalf("changed target was accepted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != want {
				t.Fatalf("target overwritten: %q %v", got, err)
			}
		})
	}
}

type cancelHostSnapshotReader struct {
	reader io.Reader
	cancel context.CancelCauseFunc
	cause  error
}

func (reader *cancelHostSnapshotReader) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	if n > 0 {
		reader.cancel(reader.cause)
	}
	return n, err
}

func TestHostGrantFileSnapshotCancelledCopyPreservesCauseAndUnrelatedFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "keep.txt"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(sourcePath, []byte(strings.Repeat("actual source\n", 10000)), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cause := errors.New("fixture cancelled during its real file read")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stage, err := stageWorkspaceFile(ctx, root, &cancelHostSnapshotReader{reader: source, cancel: cancel, cause: cause}, 1<<20)
	if stage != nil || !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("stage=%#v cause=%v", stage, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("copy cleanup touched unrelated output: %#v %v", entries, err)
	}
}
