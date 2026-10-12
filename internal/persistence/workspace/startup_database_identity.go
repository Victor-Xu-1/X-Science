package workspace

import (
	"errors"
	"os"
	"path/filepath"
)

const PublishedDatabaseFilename = "synonbiomed-v1.1.sqlite"

// StartupDatabasePath keeps the already published DB/blob authority in place.
// Changing its filename requires the verified handoff, not ordinary startup.
func StartupDatabasePath(home string) (string, error) {
	directory := filepath.Join(home, "workspace")
	canonical := filepath.Join(directory, DatabaseFilename)
	published := filepath.Join(directory, PublishedDatabaseFilename)
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return canonical, nil
	}
	if err != nil {
		return "", err
	}
	defer root.Close()
	canonicalExists, err := regularStartupDatabase(root, DatabaseFilename)
	if err != nil {
		return "", err
	}
	publishedExists, err := regularStartupDatabase(root, PublishedDatabaseFilename)
	if err != nil {
		return "", err
	}
	if canonicalExists && publishedExists {
		return "", errors.New("workspace namespace has two database authorities; verified handoff is required")
	}
	if publishedExists {
		return published, nil
	}
	return canonical, nil
}

func regularStartupDatabase(root *os.Root, name string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("workspace database authority is not a regular non-symlink file")
	}
	return true, nil
}
