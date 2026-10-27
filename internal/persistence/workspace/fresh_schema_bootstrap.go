package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errFreshSchemaNotEmpty = errors.New("workspace schema is no longer empty")

// Reserve only a new ordinary file. An existing file, URI or memory database
// retains the original migration/backup owner. A race is never an empty proof.
func reserveFreshWorkspaceFile(requested, path string, existed bool) (os.FileInfo, bool, error) {
	if existed || !plainWorkspaceFilePath(requested) {
		return nil, existed, nil
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		_, currentExists, pathErr := prepareWorkspaceDatabasePath(path)
		return nil, currentExists, pathErr
	}
	if err != nil {
		return nil, false, fmt.Errorf("reserve fresh workspace database: %w", err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		return nil, false, fmt.Errorf("inspect fresh workspace database: %w", err)
	}
	return info, false, nil
}

func plainWorkspaceFilePath(path string) bool {
	return path != "" && strings.TrimSpace(path) == path && path != ":memory:" &&
		!strings.HasPrefix(strings.ToLower(path), "file:") && !strings.Contains(path, "://") &&
		!strings.ContainsAny(path, "?\x00")
}

// This short-lived pool alone uses IMMEDIATE BeginTx. The normal writer and
// read-pool DSNs are unchanged, and no connection-local FK setting escapes it.
func (s *Store) bootstrapFreshWorkspace(ctx context.Context, path string, owned os.FileInfo, writer schemaMigrationQueryExecutor) (handled bool, returnErr error) {
	if owned == nil {
		return false, nil
	}
	if err := requireFreshWorkspaceConnectionIdentity(ctx, writer, path, owned); err != nil {
		return false, err
	}
	db, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path)+"?_txlock=immediate")
	if err != nil {
		return false, fmt.Errorf("open fresh workspace migration connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	if err := requireFreshWorkspaceConnectionIdentity(ctx, conn, path, owned); err != nil {
		return false, err
	}
	if err := configureWorkspaceSchemaConnection(ctx, conn); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return false, fmt.Errorf("prepare atomic workspace foreign keys: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, restoreErr := conn.ExecContext(cleanup, `PRAGMA foreign_keys=ON`)
		var enabled int
		if restoreErr == nil {
			restoreErr = conn.QueryRowContext(cleanup, `PRAGMA foreign_keys`).Scan(&enabled)
			if restoreErr == nil && enabled != 1 {
				restoreErr = errors.New("atomic workspace foreign keys were not restored")
			}
		}
		returnErr = errors.Join(returnErr, restoreErr)
	}()
	err = s.bootstrapFreshWorkspaceConnection(ctx, conn, func() error {
		for _, query := range []schemaMigrationQueryExecutor{writer, conn} {
			if err := requireFreshWorkspaceConnectionIdentity(ctx, query, path, owned); err != nil {
				return err
			}
		}
		return nil
	})
	for _, query := range []schemaMigrationQueryExecutor{writer, conn} {
		if identityErr := requireFreshWorkspaceConnectionIdentity(ctx, query, path, owned); identityErr != nil {
			return false, errors.Join(err, identityErr)
		}
	}
	if errors.Is(err, errFreshSchemaNotEmpty) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Verify each pinned connection reports the intended main file and that the
// reserved regular file still occupies that path. Never reopen or remove a
// changed pathname to make an uncertain identity fit the fresh path.
func requireFreshWorkspaceConnectionIdentity(ctx context.Context, query schemaMigrationQueryExecutor, path string, owned os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(owned, current) {
		return errors.New("fresh workspace database identity changed")
	}
	rows, err := query.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return err
	}
	defer rows.Close()
	mainCount := 0
	for rows.Next() {
		var sequence int
		var name, filename string
		if err := rows.Scan(&sequence, &name, &filename); err != nil {
			return err
		}
		if name == "temp" && filename == "" {
			continue
		}
		if name != "main" || filename == "" || filepath.Clean(filename) != filepath.Clean(path) {
			return errors.New("fresh workspace connection target is not the reserved main database")
		}
		actual, err := os.Lstat(filename)
		if err != nil || !actual.Mode().IsRegular() || !os.SameFile(owned, actual) {
			return errors.New("fresh workspace connection file identity changed")
		}
		mainCount++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if mainCount != 1 {
		return errors.New("fresh workspace connection has no unique main database")
	}
	return nil
}

func (s *Store) bootstrapFreshWorkspaceConnection(ctx context.Context, conn *sql.Conn, checkIdentity func() error) error {
	return withSchemaMigrationTransaction(ctx, conn, "atomic fresh workspace schema", func(tx *sql.Tx) error {
		if checkIdentity != nil {
			if err := checkIdentity(); err != nil {
				return err
			}
		}
		var objects, schemaVersion, userVersion, applicationID, foreignKeys int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
			return err
		}
		for _, item := range []struct {
			query string
			value *int
		}{{`PRAGMA schema_version`, &schemaVersion}, {`PRAGMA user_version`, &userVersion},
			{`PRAGMA application_id`, &applicationID}, {`PRAGMA foreign_keys`, &foreignKeys}} {
			if err := tx.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
				return err
			}
		}
		if objects != 0 || schemaVersion != 0 || userVersion != 0 || applicationID != 0 {
			return errFreshSchemaNotEmpty
		}
		if foreignKeys != 0 {
			return errors.New("atomic fresh workspace requires its isolated FK setup")
		}
		if err := prepareSchemaJournal(ctx, tx); err != nil {
			return err
		}
		if err := s.prepareLegacyWorkspaceSchemaBody(ctx, tx); err != nil {
			return err
		}
		if err := applyVersionedSchemaMigrationsInTx(ctx, tx, s.now, workspaceSchemaVersion); err != nil {
			return err
		}
		return requireMigrationForeignKeys(ctx, tx)
	})
}

func requireMigrationForeignKeys(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var failures int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&failures); err != nil {
		return err
	}
	if failures != 0 {
		return errors.New("workspace schema migration left an invalid foreign key graph")
	}
	return nil
}
