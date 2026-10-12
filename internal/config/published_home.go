package config

import (
	"errors"
	"os"
	"path/filepath"

	workspace "x-science/internal/persistence/workspace"
)

// Used only for the real default when no explicit config/env/durable pointer
// selects a home. Never guesses sibling homes for an operator-provided path.
func retainedPublishedDefaultHome(canonical string) (string, bool, error) {
	published := filepath.Join(filepath.Dir(canonical), ".synon-go")
	root, err := os.OpenRoot(published)
	if errors.Is(err, os.ErrNotExist) {
		return canonical, false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	oldInfo, err := root.Lstat(filepath.Join("workspace", workspace.PublishedDatabaseFilename))
	if errors.Is(err, os.ErrNotExist) {
		return canonical, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !oldInfo.Mode().IsRegular() || oldInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("published default datastore is unsafe")
	}
	newDatabase, err := workspace.StartupDatabasePath(canonical)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(newDatabase); err == nil {
		return "", false, errors.New("default namespace has two datastore authorities; select the verified data-directory pointer")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	resolved, err := filepath.EvalSymlinks(published)
	if err != nil || resolved != filepath.Clean(published) {
		return "", false, errors.New("published default home is linked or unavailable")
	}
	return published, true, nil
}
