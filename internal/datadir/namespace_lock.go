package datadir

import (
	"errors"
	"os"
	"path/filepath"
)

// The inode is durable but ownership is the OS advisory lock, not file
// existence or its text. Never unlink a lock inode: another process could
// otherwise lock an obsolete inode while a new writer opens its replacement.
func lockNamespacePointer(path string) (*os.File, error) {
	if _, err := canonicalExistingDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	file, err := openNamespaceLock(path)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > namespaceFileLimit {
		return nil, errors.New("default pointer lock is not a bounded regular file")
	}
	if err := namespaceOwned(path, info, true); err != nil {
		return nil, err
	}
	if err := acquireNamespaceLock(file); err != nil {
		return nil, err
	}
	actual, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, actual) || !actual.Mode().IsRegular() {
		return nil, errors.New("default pointer lock inode changed during acquisition")
	}
	failed = false
	return file, nil
}
