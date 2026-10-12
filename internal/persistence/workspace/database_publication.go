package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"x-science/internal/sqlitebackup"
)

// PublishCanonicalWorkspaceDatabase drains a published legacy workspace database
// (and its blob root) into the canonical path when the canonical file is absent.
// It never renames a sibling legacy file while a separate canonical file already
// exists in the same directory.
func PublishCanonicalWorkspaceDatabase(canonicalPath string, homeRoots []string) error {
	canonicalPath = strings.TrimSpace(canonicalPath)
	if canonicalPath == "" {
		return errors.New("canonical workspace database path is required")
	}
	absolute, err := filepath.Abs(canonicalPath)
	if err != nil {
		return fmt.Errorf("resolve canonical workspace database path: %w", err)
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("canonical workspace database must be a regular file")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect canonical workspace database: %w", err)
	}
	legacyPath, err := locatePublishedLegacyWorkspaceDatabase(homeRoots)
	if err != nil || legacyPath == "" {
		return err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("prepare canonical workspace database parent: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || resolvedParent != parent {
		return errors.New("canonical workspace database parent must not contain symbolic links")
	}
	if err := drainAndPublishWorkspaceDatabase(legacyPath, absolute); err != nil {
		return err
	}
	return nil
}

func locatePublishedLegacyWorkspaceDatabase(homeRoots []string) (string, error) {
	for _, root := range homeRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve legacy workspace home: %w", err)
		}
		candidate := filepath.Join(absolute, "workspace", legacyWorkspaceDatabaseFilename)
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect legacy workspace database: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("legacy workspace database must be a regular file")
		}
		return candidate, nil
	}
	return "", nil
}

func drainAndPublishWorkspaceDatabase(legacyPath, canonicalPath string) error {
	databaseURL := sqliteFileDatabaseURL(legacyPath)
	db, err := sql.Open(sqliteDriver, databaseURL+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open legacy workspace database: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("drain legacy workspace WAL: %w", err)
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire legacy workspace publication connection: %w", err)
	}
	defer connection.Close()
	stagingRoot := filepath.Join(filepath.Dir(canonicalPath), ".workspace-publication-staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return fmt.Errorf("prepare workspace publication staging root: %w", err)
	}
	result, err := sqlitebackup.BackupFromConn(ctx, connection, stagingRoot)
	if err != nil {
		return fmt.Errorf("backup legacy workspace database for publication: %w", err)
	}
	if err := os.Rename(result.Path, canonicalPath); err != nil {
		_ = os.RemoveAll(result.Directory)
		return fmt.Errorf("publish canonical workspace database: %w", err)
	}
	_ = os.RemoveAll(result.Directory)
	_ = os.Remove(stagingRoot)
	if err := os.Chmod(canonicalPath, 0o600); err != nil {
		return fmt.Errorf("secure published workspace database: %w", err)
	}
	if err := publishWorkspaceBlobRoot(legacyPath, canonicalPath); err != nil {
		return err
	}
	removeLegacyWorkspaceSidecars(legacyPath)
	return nil
}

func publishWorkspaceBlobRoot(legacyPath, canonicalPath string) error {
	legacyBlobs := legacyPath + ".blobs"
	canonicalBlobs := canonicalPath + ".blobs"
	legacyInfo, legacyErr := os.Lstat(legacyBlobs)
	if errors.Is(legacyErr, os.ErrNotExist) {
		return nil
	}
	if legacyErr != nil {
		return fmt.Errorf("inspect legacy workspace blob root: %w", legacyErr)
	}
	if !legacyInfo.IsDir() || legacyInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy workspace blob root must be a real directory")
	}
	if _, err := os.Lstat(canonicalBlobs); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect canonical workspace blob root: %w", err)
	}
	if err := os.Rename(legacyBlobs, canonicalBlobs); err != nil {
		return fmt.Errorf("publish workspace blob root: %w", err)
	}
	return nil
}

func removeLegacyWorkspaceSidecars(legacyPath string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(legacyPath + suffix)
	}
}
