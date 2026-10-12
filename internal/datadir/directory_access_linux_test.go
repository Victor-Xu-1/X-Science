//go:build linux

package datadir

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"x-science/internal/fsidentity"
)

func TestCanonicalDirectoryOpenNeverReturnsOutsideTreeDuringParentSwap(t *testing.T) {
	inside, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(inside, "parent")
	target := filepath.Join(parent, "target")
	for _, path := range []string{target, filepath.Join(outside, "target")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		held := parent + "-held"
		for {
			select {
			case <-done:
				return
			default:
			}
			if os.Rename(parent, held) != nil {
				continue
			}
			_ = os.Symlink(outside, parent)
			runtime.Gosched()
			_ = os.Remove(parent)
			_ = os.Rename(held, parent)
			runtime.Gosched()
		}
	}()
	defer func() { close(done); workers.Wait() }()
	for range 1000 {
		root, err := openCanonicalDirectory(target)
		if err != nil {
			continue
		}
		file, err := root.Open(".")
		if err == nil {
			final, finalErr := fsidentity.FinalPath(file)
			_ = file.Close()
			if finalErr == nil && (final == outside || strings.HasPrefix(final, outside+string(filepath.Separator))) {
				_ = root.Close()
				t.Fatalf("opened outside directory returned: %s", final)
			}
		}
		_ = root.Close()
	}
}
