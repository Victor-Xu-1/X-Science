package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWebFSWorkspaceAuthorizationPrecedesFilesystemAccess(t *testing.T) {
	srv := &Server{fileRoot: t.TempDir()}
	outside := t.TempDir()
	for _, workspace := range []string{outside, filepath.Join(outside, "missing")} {
		for _, write := range []bool{false, true} {
			_, err := srv.resolveWebFSPath("owner", ".", workspace, write, true, false)
			if !errors.Is(err, errWebFSForbidden) {
				t.Fatalf("workspace=%q write=%v: expected authorization failure, got %v", workspace, write, err)
			}
		}
	}
}

func TestWebFSWorkspaceScopeAndSymlinkEscape(t *testing.T) {
	srv := &Server{fileRoot: t.TempDir()}
	root, err := srv.webFSTempRoot("owner")
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "note.txt")
	if err := os.WriteFile(file, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	access, err := srv.resolveWebFSPath("owner", "note.txt", nested, false, true, false)
	if err != nil || access.Root != nested || access.Target != file {
		t.Fatalf("nested access=%+v err=%v", access, err)
	}
	if _, err := srv.resolveWebFSPath("owner", "../sibling.txt", nested, true, false, false); !errors.Is(err, errWebFSForbidden) {
		t.Fatalf("workspace scope escaped: %v", err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{link, filepath.Join(link, "missing")} {
		if _, err := srv.resolveWebFSPath("owner", ".", workspace, false, true, false); err == nil {
			t.Fatalf("accepted symlink escape %q", workspace)
		}
	}
}
