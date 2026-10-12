package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"x-science/internal/sqlitebackup"
)

const workspaceDatabaseHandoffLockName = ".workspace-database-handoff.lock"

// PublishCanonicalWorkspaceDatabase drains a published legacy workspace database
// (and its blob root) into the canonical path when handoff is required. Open
// never discovers sibling legacy files on its own.
func PublishCanonicalWorkspaceDatabase(canonicalPath string, homeRoots []string) error {
	canonicalPath = strings.TrimSpace(canonicalPath)
	if canonicalPath == "" {
		return errors.New("canonical workspace database path is required")
	}
	absolute, err := filepath.Abs(canonicalPath)
	if err != nil {
		return fmt.Errorf("resolve canonical workspace database path: %w", err)
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
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("canonical workspace database must be a regular file")
		}
		required, err := legacyWorkspaceHandoffRequired(absolute, legacyPath)
		if err != nil {
			return err
		}
		if !required {
			return nil
		}
		if err := removeWorkspaceDatabaseTree(absolute); err != nil {
			return fmt.Errorf("remove orphan canonical workspace database before handoff: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect canonical workspace database: %w", err)
	}
	unlock, err := acquireWorkspaceDatabaseHandoffLock(parent)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := os.Lstat(legacyPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("reinspect legacy workspace database: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		required, err := legacyWorkspaceHandoffRequired(absolute, legacyPath)
		if err != nil {
			return err
		}
		if !required {
			return nil
		}
		if err := removeWorkspaceDatabaseTree(absolute); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reinspect canonical workspace database: %w", err)
	}
	if err := retainWorkspaceHandoffRecovery(parent, legacyPath); err != nil {
		return err
	}
	return drainAndPublishWorkspaceDatabase(legacyPath, absolute)
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

func legacyWorkspaceHandoffRequired(canonicalPath, legacyPath string) (bool, error) {
	legacyContent, err := workspaceDatabaseHasUserContent(legacyPath)
	if err != nil {
		return false, err
	}
	if !legacyContent {
		return false, nil
	}
	canonicalContent, err := workspaceDatabaseHasUserContent(canonicalPath)
	if err != nil {
		return false, err
	}
	if canonicalContent {
		return false, errors.New("published legacy workspace database remains while a separate canonical workspace database already owns live state")
	}
	return true, nil
}

func workspaceDatabaseHasUserContent(path string) (bool, error) {
	databaseURL := sqliteFileDatabaseURL(path)
	db, err := sql.Open(sqliteDriver, databaseURL+"?_pragma=busy_timeout(5000)&mode=ro")
	if err != nil {
		return false, fmt.Errorf("open workspace database for handoff inspection: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, query := range []string{
		`SELECT 1 FROM projects LIMIT 1`,
		`SELECT 1 FROM frames LIMIT 1`,
		`SELECT 1 FROM memories LIMIT 1`,
	} {
		var marker int
		if err := db.QueryRowContext(ctx, query).Scan(&marker); err == nil {
			return true, nil
		}
	}
	return false, nil
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
	if _, err := os.Lstat(canonicalPath); !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(result.Directory)
		return errors.New("canonical workspace database appeared during publication staging")
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

func retainWorkspaceHandoffRecovery(workspaceParent, legacyPath string) error {
	recoveryRoot := filepath.Join(workspaceParent, ".workspace-database-handoff-recovery")
	if err := os.MkdirAll(recoveryRoot, 0o700); err != nil {
		return fmt.Errorf("prepare workspace handoff recovery root: %w", err)
	}
	digest := sha256.Sum256([]byte(filepath.Clean(legacyPath)))
	target := filepath.Join(recoveryRoot, hex.EncodeToString(digest[:]))
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace handoff recovery target: %w", err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return fmt.Errorf("create workspace handoff recovery directory: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := legacyPath + suffix
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect legacy workspace handoff source %q: %w", source, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("legacy workspace handoff source %q is not a regular file", source)
		}
		if err := copyRegularFile(source, filepath.Join(target, filepath.Base(source))); err != nil {
			return err
		}
	}
	blobSource := legacyPath + ".blobs"
	if info, err := os.Lstat(blobSource); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := copyDirectoryTree(blobSource, filepath.Join(target, filepath.Base(blobSource))); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect legacy workspace blob recovery source: %w", err)
	}
	return nil
}

func copyRegularFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open handoff recovery source: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create handoff recovery copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(target)
		return fmt.Errorf("copy handoff recovery source: %w", err)
	}
	return out.Close()
}

func copyDirectoryTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("handoff recovery source %q is not a regular file", path)
		}
		return copyRegularFile(path, destination)
	})
}

func removeWorkspaceDatabaseTree(canonicalPath string) error {
	for _, suffix := range []string{"", "-wal", "-shm", ".blobs"} {
		if err := os.RemoveAll(canonicalPath + suffix); err != nil {
			return err
		}
	}
	return nil
}

func removeLegacyWorkspaceSidecars(legacyPath string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(legacyPath + suffix)
	}
}
