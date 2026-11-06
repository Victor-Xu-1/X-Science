package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestSessionRunnerInferredDependenciesIgnoreInvalidSourcePathShape(t *testing.T) {
	for _, test := range []struct{ name, constant string }{
		{"ordinary long text", strings.Repeat("An ordinary sentence contains punctuation. ", 16) + "End"},
		{"data text below regular file", "regular.dat/description.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"regular.dat", "input.dat"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic data\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, probeErr := os.Stat(filepath.Join(root, filepath.FromSlash(test.constant)))
			if probeErr == nil {
				t.Fatal("synthetic non-path unexpectedly names an existing file")
			}
			var errno syscall.Errno
			if errors.As(probeErr, &errno) {
				t.Logf("synthetic inferred candidate stat errno=%d", errno)
			}
			store := openRunnerArtifactCompletionStore(t)
			const stream = "synthetic-source-constants"
			artifactID := streamArtifactIDFor(stream, "script.py")
			source := "DESCRIPTION = " + strconv.Quote(test.constant) + "\nINPUT = \"input.dat\"\n"
			if !slices.Contains(sessionRunnerSourceLocalDependencyCandidates(source), test.constant) {
				t.Fatal("synthetic data did not enter the actual inferred-candidate path")
			}
			_, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
				ArtifactID: artifactID, ProjectID: "project-a", Name: "script.py", Kind: "text/x-python",
				Content: []byte(source), CreatedBy: "runner",
			})
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{fileRoot: root, workspaceStore: store}
			run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: transcriptstore.Stream{UID: stream}}}
			commits := []transcriptstore.ArtifactReferenceInput{{ArtifactID: artifactID, VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}}
			missing, err := server.sessionRunnerMissingLocalArtifactDependencies("project-a", run, commits, "Done.")
			if err != nil {
				var actual syscall.Errno
				errors.As(err, &actual)
				t.Fatalf("ordinary source data blocked dependency inference: errno=%d type=%T", actual, err)
			}
			if !reflect.DeepEqual(missing, []string{"input.dat"}) {
				t.Fatalf("real dependency was lost: %#v", missing)
			}
			if !errors.Is(probeErr, os.ErrNotExist) && !sessionRunnerInvalidInferredDependencyPath(probeErr) {
				t.Fatal("native path-shape probe was not recognized by the local classifier")
			}
		})
	}
}

func TestSessionRunnerInferredDependencyPathPreservesHardErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"permission", fs.ErrPermission},
		{"io", syscall.EIO},
		{"disk full", syscall.ENOSPC},
		{"similar error text", errors.New("file name too long")},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fmt.Errorf("filesystem probe: %w", &fs.PathError{Op: "stat", Path: "synthetic.dat", Err: test.err})
			if sessionRunnerInvalidInferredDependencyPath(err) {
				t.Fatal("hard filesystem failure was classified as a non-path source string")
			}
		})
	}
}

func TestSessionRunnerInferredDependencyProbePropagatesPermissionFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory-permission fixture; native error classification is checked separately")
	}
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "input.dat"), []byte("synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(private, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(private, 0o700) })
	if _, err := os.Stat(filepath.Join(private, "input.dat")); !errors.Is(err, fs.ErrPermission) {
		t.Skip("fixture user can still inspect the denied directory")
	}
	err := (&Server{fileRoot: root}).addMissingSessionRunnerLocalArtifactDependency(
		"synthetic-permission", nil, map[string]struct{}{}, "private/input.dat")
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("real permission failure was not returned: %T", err)
	}
}

func TestSessionRunnerInferredDependencyScanPreservesPrimaryArtifactFailure(t *testing.T) {
	store := openRunnerArtifactCompletionStore(t)
	_, version, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "synthetic-primary", ProjectID: "project-a", Name: "script.py", Kind: "text/x-python",
		Content: []byte("DESCRIPTION = \"source.data\"\n"), CreatedBy: "runner",
	})
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: transcriptstore.Stream{UID: "synthetic-primary"}}}
	commits := []transcriptstore.ArtifactReferenceInput{{ArtifactID: "synthetic-primary", VersionID: version.ID, Relation: transcriptstore.ArtifactRelationProduced}}
	_, err = (&Server{fileRoot: t.TempDir(), workspaceStore: store}).sessionRunnerMissingLocalArtifactDependencies(
		"project-b", run, commits, "Done.")
	if err == nil {
		t.Fatal("primary artifact authority failure was suppressed")
	}
}
