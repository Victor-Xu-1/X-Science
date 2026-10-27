package workspace

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFreshWorkspaceBootstrapAtomicContracts(t *testing.T) {
	t.Run("canonical schema and user files", testFreshWorkspaceCanonicalSchema)
	t.Run("late SQL failure and cancellation rollback", testFreshWorkspaceRollback)
	t.Run("locked recheck preserves another initializer", testFreshWorkspaceLockedRecheck)
	t.Run("concurrent Open", testConcurrentFreshWorkspaceOpen)
	t.Run("existing partial and unsupported paths", testFreshWorkspaceFallback)
	t.Run("replaced placeholder is rejected", testFreshWorkspaceReplacedFile)
}

func requireFreshSchemaStatus(t *testing.T, store *Store) {
	t.Helper()
	got, err := store.SchemaStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want, err := ExpectedSchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentVersion != want.CurrentVersion || len(got.Migrations) != len(want.Migrations) {
		t.Fatalf("migration journal shape differs: got=%d/%d want=%d/%d", got.CurrentVersion, len(got.Migrations), want.CurrentVersion, len(want.Migrations))
	}
	for index, row := range got.Migrations {
		expected := want.Migrations[index]
		if row.Version != expected.Version || row.Name != expected.Name || row.Checksum != expected.Checksum || row.RemediationID != expected.RemediationID {
			t.Fatalf("migration identity %d differs", index)
		}
	}
	var enabled int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("ordinary writer FK state=%d err=%v", enabled, err)
	}
	if err := requireMigrationForeignKeys(t.Context(), store.db); err != nil {
		t.Fatal(err)
	}
}

func freshSchemaObjects(t *testing.T, db *sql.DB) [][4]string {
	t.Helper()
	rows, err := db.Query(`SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][4]string
	for rows.Next() {
		var item [4]string
		if err := rows.Scan(&item[0], &item[1], &item[2], &item[3]); err != nil {
			t.Fatal(err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func testFreshWorkspaceCanonicalSchema(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "user-file.txt")
	if err := os.WriteFile(sentinel, []byte("preserved user content"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "workspace.db")
	fresh, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if fresh.schemaBackupPath != "" {
		t.Fatal("new database unexpectedly became an upgrade")
	}
	requireFreshSchemaStatus(t, fresh)
	// A pre-existing file deliberately exercises the unchanged full upgrade
	// owner. It is not a copied or cached schema template.
	legacyPath := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(legacyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := Open(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	requireFreshSchemaStatus(t, legacy)
	if !reflect.DeepEqual(freshSchemaObjects(t, fresh.db), freshSchemaObjects(t, legacy.db)) {
		t.Fatal("atomic fresh schema differs from the original per-version schema")
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	requireFreshSchemaStatus(t, reopened)
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "preserved user content" {
		t.Fatalf("bootstrap altered an unrelated workspace file: %v", err)
	}
}

func freshBootstrapTestConnection(t *testing.T, path string) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open(sqliteDriver, sqliteFileDatabaseURL(path)+"?_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := configureWorkspaceSchemaConnection(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	return db, conn
}

func testFreshWorkspaceRollback(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		t.Run(map[bool]string{false: "late real SQL error", true: "cancelled shared transaction"}[cancellation], func(t *testing.T) {
			db, conn := freshBootstrapTestConnection(t, filepath.Join(t.TempDir(), "rollback.db"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := &Store{now: time.Now}
			err := withSchemaMigrationTransaction(ctx, conn, "fresh rollback contract", func(tx *sql.Tx) error {
				if err := prepareSchemaJournal(ctx, tx); err != nil {
					return err
				}
				if err := store.prepareLegacyWorkspaceSchemaBody(ctx, tx); err != nil {
					return err
				}
				if cancellation {
					cancel()
					return ctx.Err()
				}
				if err := applyVersionedSchemaMigrationsInTx(ctx, tx, time.Now, workspaceSchemaVersion); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO missing_bootstrap_contract_table VALUES (1)`)
				return err
			})
			if err == nil {
				t.Fatal("precommit failure unexpectedly committed")
			}
			// Return the borrowed connection so the one-connection pool can read.
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if got := freshSchemaObjects(t, db); len(got) != 0 {
				t.Fatalf("partial bootstrap survived rollback: %d objects", len(got))
			}
		})
	}
}

func testFreshWorkspaceLockedRecheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	_, first := freshBootstrapTestConnection(t, path)
	_, second := freshBootstrapTestConnection(t, path)
	owner, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Rollback()
	if _, err := owner.ExecContext(t.Context(), `CREATE TABLE user_owned(value TEXT); INSERT INTO user_owned VALUES ('preserved')`); err != nil {
		t.Fatal(err)
	}
	ready, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(ready)
		result <- (&Store{now: time.Now}).bootstrapFreshWorkspaceConnection(t.Context(), second, nil)
	}()
	<-ready
	if err := owner.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, errFreshSchemaNotEmpty) {
		t.Fatalf("initializer did not reject the locked nonempty state: %v", err)
	}
	var value string
	if err := second.QueryRowContext(t.Context(), `SELECT value FROM user_owned`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("other initializer data changed: %q %v", value, err)
	}
	var journals int
	if err := second.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_schema WHERE name='workspace_schema_migrations'`).Scan(&journals); err != nil || journals != 0 {
		t.Fatalf("fallback created a journal: %d %v", journals, err)
	}
}

func testConcurrentFreshWorkspaceOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	type outcome struct {
		store *Store
		err   error
	}
	start, results := make(chan struct{}), make(chan outcome, 2)
	for range 2 {
		go func() { <-start; store, err := Open(path); results <- outcome{store, err} }()
	}
	close(start)
	var firstError error
	for range 2 {
		item := <-results
		if item.err != nil {
			firstError = errors.Join(firstError, item.err)
			continue
		}
		t.Cleanup(func() { _ = item.store.Close() })
		requireFreshSchemaStatus(t, item.store)
	}
	if firstError != nil {
		t.Fatal(firstError)
	}
}

func testFreshWorkspaceReplacedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reserved.db")
	owned, _, err := reserveFreshWorkspaceFile(path, path, false)
	if err != nil || owned == nil {
		t.Fatalf("reserve owned fixture: %v", err)
	}
	if err := os.Rename(path, path+".preserved"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE replacement_owner(value TEXT); INSERT INTO replacement_owner VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Store{now: time.Now}).bootstrapFreshWorkspace(t.Context(), path, owned, db); err == nil {
		t.Fatal("a changed file identity was admitted as the reserved fresh DB")
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM replacement_owner`).Scan(&value); err != nil || value != "kept" {
		t.Fatalf("replacement file changed: %q %v", value, err)
	}
	if _, err := os.Stat(path + ".preserved"); err != nil {
		t.Fatal("original reserved file was removed", err)
	}
}

func testFreshWorkspaceFallback(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:?cache=shared", "file:/tmp/existing.db", "https://example.test/db", "", " spaced "} {
		if plainWorkspaceFilePath(path) {
			t.Fatalf("unsupported path entered fresh bootstrap: %q", path)
		}
	}
	path := filepath.Join(t.TempDir(), "partial.db")
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE user_owned(value TEXT); INSERT INTO user_owned VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.schemaBackupPath == "" {
		t.Fatal("pre-existing partial DB bypassed the original backup owner")
	}
	var value string
	if err := store.db.QueryRow(`SELECT value FROM user_owned`).Scan(&value); err != nil || value != "kept" {
		t.Fatalf("legacy data changed: %q %v", value, err)
	}
	requireFreshSchemaStatus(t, store)
}
