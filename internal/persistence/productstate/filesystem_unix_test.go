//go:build !windows

package productstate

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type foreignFileInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (info foreignFileInfo) Sys() any { return &info.stat }

func TestHiddenNamespaceForeignOwnershipAndCrossProcessLockAreRejected(t *testing.T) {
	root := t.TempDir()
	path := privateFixture(t, root, "fixture")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid++
	if err := checkOwner(path, foreignFileInfo{info, stat}); err == nil {
		t.Fatal("foreign UID accepted")
	}
	stat = *info.Sys().(*syscall.Stat_t)
	stat.Gid++
	if err := checkOwner(path, foreignFileInfo{info, stat}); err == nil {
		t.Fatal("foreign GID accepted")
	}
	first, err := lockNamespace(context.Background(), filepath.Join(root, "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlockNamespace(first)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if second, err := lockNamespace(ctx, filepath.Join(root, "lock")); err == nil {
		unlockNamespace(second)
		t.Fatal("competing lock ignored deadline")
	}
}

func TestHiddenNamespaceReadOnlyOwnedModesRemainExact(t *testing.T) {
	root := t.TempDir()
	source := privateFixture(t, root, ".synon/teams/readonly.json")
	if err := os.Chmod(source, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(source), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(filepath.Dir(source), 0o700)
		os.Chmod(filepath.Join(root, HiddenDirectory, "teams"), 0o700)
	})
	if err := UpgradeHiddenNamespace(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Join(root, HiddenDirectory, "teams"), 0o500}, {filepath.Join(root, HiddenDirectory, "teams/readonly.json"), 0o400}} {
		info, err := os.Stat(test.path)
		if err != nil || info.Mode().Perm() != test.mode {
			t.Fatal("owned read-only mode changed")
		}
	}
}

func TestHiddenNamespaceUnsafeUnixPermissionsFailBeforeEffects(t *testing.T) {
	root := t.TempDir()
	path := privateFixture(t, root, ".synon/teams/unsafe")
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := UpgradeHiddenNamespace(context.Background(), root); err == nil {
		t.Fatal("unsafe POSIX mode accepted")
	}
	if _, err := os.Stat(filepath.Join(root, HiddenDirectory)); !os.IsNotExist(err) {
		t.Fatal("unsafe source initialized canonical state")
	}
}
