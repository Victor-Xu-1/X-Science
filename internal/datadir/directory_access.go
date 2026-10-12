package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"x-science/internal/fsidentity"
)

// Open the verified existing directory and compare the actual capability to
// its pre-open identity. All subsequent writes stay anchored to that root.
func openCanonicalDirectory(path string) (*os.Root, error) {
	absolute, err := canonicalExistingDirectory(path)
	if err != nil {
		return nil, err
	}
	// absolute passed cleanAbsolutePath and symlink rejection in canonicalExistingDirectory.
	expected, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(expected, actual) {
		_ = root.Close()
		return nil, errors.New("directory changed while opening its capability")
	}
	file, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	final, pathErr := fsidentity.FinalPath(file)
	closeErr := file.Close()
	match := final == absolute
	if runtime.GOOS == "windows" {
		match = strings.EqualFold(final, absolute)
	}
	if pathErr != nil || closeErr != nil || !match {
		_ = root.Close()
		return nil, errors.New("opened directory path differs from its canonical target")
	}
	return root, nil
}

// Validate each existing ancestor before creating anything. MkdirAll on an
// unverified path can write through a symlink even if validation later fails.
func createCanonicalDirectory(path string) error {
	absolute, err := cleanAbsolutePath(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(absolute); err == nil {
		_, err = canonicalExistingDirectory(absolute)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return errors.New("directory has no existing parent")
	}
	if err := createCanonicalDirectory(parent); err != nil {
		return err
	}
	root, err := openCanonicalDirectory(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir(filepath.Base(absolute), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	_, err = canonicalExistingDirectory(absolute)
	return err
}

// Failure cleanup may remove only the same empty target that this operation
// opened. A changed parent or replacement leaf is preserved, never followed.
func removeCreatedEmptyDirectory(path string, owned *os.Root) error {
	expected, err := owned.Stat(".")
	if err != nil {
		return err
	}
	parent, err := openCanonicalDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(path)
	actual, err := parent.Lstat(name)
	if err != nil || !actual.IsDir() || !os.SameFile(expected, actual) {
		return errors.New("created target changed before cleanup")
	}
	return parent.Remove(name)
}
