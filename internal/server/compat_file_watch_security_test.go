package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompatFileWatchAuthorizesBeforePathProbe(t *testing.T) {
	watches := newCompatFileWatchSet(context.Background(), t.TempDir(), make(chan map[string]any, 1), func(string) error { return errors.New("denied") })
	defer watches.Close()
	response := watches.Control(map[string]any{"type": "file_watch", "path": "not-present"})
	if response["code"] != "FILE_WATCH_REJECTED" || len(watches.watches) != 0 {
		t.Fatalf("unowned watch admitted: %#v", response)
	}
}

func TestCompatFileWatchRootRejectsPostValidationParentSymlink(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	watches := newCompatFileWatchSet(context.Background(), root, make(chan map[string]any, 1), func(string) error { return nil })
	defer watches.Close()
	if err := os.Rename(nested, filepath.Join(root, "held")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, nested); err != nil {
		t.Fatal(err)
	}
	if info := watches.statPath(filepath.Join(nested, "private")); info.exists {
		t.Fatal("watch exposed metadata outside its opened root")
	}
}

func TestCompatFileWatchUsesExistingOwnerAuthorityAndReportsOwnedChanges(t *testing.T) {
	srv := &Server{fileRoot: t.TempDir()}
	owned, err := srv.webFSTempRoot("owner")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := srv.webFSTempRoot("foreign")
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{owned, foreign} {
		if err := os.WriteFile(filepath.Join(root, "result"), []byte("initial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	events := make(chan map[string]any, 2)
	watches := newCompatFileWatchSet(context.Background(), srv.fileRoot, events, func(path string) error {
		_, err := srv.resolveWebFSPath("owner", path, "", false, false, false)
		return err
	})
	defer watches.Close()
	if response := watches.Control(map[string]any{"type": "file_watch", "path": filepath.Join(foreign, "result")}); response["code"] != "FILE_WATCH_REJECTED" {
		t.Fatalf("foreign metadata watch accepted: %#v", response)
	}
	path := filepath.Join(owned, "result")
	if response := watches.Control(map[string]any{"type": "file_watch", "path": path}); response != nil {
		t.Fatalf("owned watch rejected: %#v", response)
	}
	if err := os.WriteFile(path, []byte("changed actual content"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event["type"] != "file_changed" || event["path"] != path {
			t.Fatalf("owned change not reported: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owned file watch did not report its actual change")
	}
	if response := watches.Control(map[string]any{"type": "file_unwatch", "path": path}); response != nil {
		t.Fatal(response)
	}
	watches.mu.Lock()
	remaining := len(watches.watches)
	watches.mu.Unlock()
	if remaining != 0 {
		t.Fatal("unwatch left an active registration")
	}
}
