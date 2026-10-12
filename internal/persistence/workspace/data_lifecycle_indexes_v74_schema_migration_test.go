package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func dataLifecycleIndexesV73Fixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	ctx := context.Background()
	if err := prepareSchemaJournal(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.prepareLegacyWorkspaceSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 73); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func TestDataLifecycleIndexesV74UpgradePreservesDataAndJournal(t *testing.T) {
	store, path := dataLifecycleIndexesV73Fixture(t)
	seedDataLifecycleIndexReferences(t, store)
	ctx := context.Background()
	before, err := store.SchemaStatus(ctx)
	if err != nil || before.CurrentVersion != 73 {
		t.Fatalf("schema before upgrade=%#v err=%v", before, err)
	}
	beforeRows := dataLifecycleIndexRows(t, store.db)
	beforePlan := dataLifecycleSnapshotDeletePlan(t, store.db)
	if !strings.Contains(beforePlan, "SCAN provenance") {
		t.Fatalf("schema 73 fixture did not reproduce missing index plan: %s", beforePlan)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if upgraded.schemaBackupPath == "" {
		t.Fatal("schema 73 upgrade did not retain its pre-upgrade backup")
	}
	backup, err := sql.Open(sqliteDriver, upgraded.schemaBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var backupVersion, backupIndexes int
	if err := backup.QueryRow(`SELECT MAX(version) FROM workspace_schema_migrations`).Scan(&backupVersion); err != nil || backupVersion != 73 {
		t.Fatalf("backup version=%d err=%v", backupVersion, err)
	}
	if err := backup.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN
		('artifact_version_provenance_lineage_snapshot_idx','artifact_version_provenance_env_snapshot_idx')`).Scan(&backupIndexes); err != nil || backupIndexes != 0 {
		t.Fatalf("backup indexes=%d err=%v", backupIndexes, err)
	}
	if got := dataLifecycleIndexRows(t, backup); got != beforeRows {
		t.Fatal("pre-upgrade backup changed retained data")
	}
	assertDataLifecycleSnapshotIndexes(t, upgraded.db)
	assertDataLifecycleIndexedPlan(t, upgraded.db)
	after, err := upgraded.SchemaStatus(ctx)
	if err != nil || after.CurrentVersion != workspaceSchemaVersion || len(after.Migrations) != workspaceSchemaVersion {
		t.Fatalf("schema after upgrade=%#v err=%v", after, err)
	}
	if !reflect.DeepEqual(after.Migrations[:73], before.Migrations) {
		t.Fatal("upgrade changed a historical migration identity or timestamp")
	}
	if got := dataLifecycleIndexRows(t, upgraded.db); got != beforeRows {
		t.Fatal("index migration changed retained workspace data")
	}
	if err := applyVersionedSchemaMigrations(ctx, upgraded.db, time.Now); err != nil {
		t.Fatalf("idempotent migration retry: %v", err)
	}
	repeated, err := upgraded.SchemaStatus(ctx)
	if err != nil || !reflect.DeepEqual(repeated, after) {
		t.Fatalf("retry changed schema journal: %#v err=%v", repeated, err)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.schemaBackupPath != "" {
		t.Fatalf("already upgraded restart created a new backup: %s", reopened.schemaBackupPath)
	}
	restarted, err := reopened.SchemaStatus(ctx)
	if err != nil || !reflect.DeepEqual(restarted, after) || dataLifecycleIndexRows(t, reopened.db) != beforeRows {
		t.Fatalf("restart changed schema or data: %#v err=%v", restarted, err)
	}
	assertDataLifecycleSnapshotIndexes(t, reopened.db)
}

func TestDataLifecycleIndexesV74FreshSchemaRetainsEitherReferenceAndSharedHashes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.schemaBackupPath != "" {
		t.Fatal("fresh schema unexpectedly created an upgrade backup")
	}
	assertDataLifecycleSnapshotIndexes(t, store.db)
	assertDataLifecycleIndexedPlan(t, store.db)
	now := seedDataLifecycleIndexReferences(t, store)
	store.now = func() time.Time { return now }
	report, err := store.SweepDataLifecycle(context.Background(), DefaultDataLifecyclePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if report.ContentSnapshots != 1 || report.EphemeralArtifacts != 0 {
		t.Fatalf("snapshot reference sweep=%#v", report)
	}
	assertLifecycleRowCount(t, store, "content_snapshots", "hash='orphan-old'", 0)
	assertLifecycleRowCount(t, store, "content_snapshots", "hash IN ('lineage-only','env-only','shared','recent','at-cutoff','')", 6)
	assertLifecycleRowCount(t, store, "artifact_versions", "1=1", 5)
	assertLifecycleRowCount(t, store, "artifact_version_provenance", "1=1", 5)
	assertLifecycleRowCount(t, store, "frames", "id='active-root' AND status='processing'", 1)
	var foreignKeyFailures int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyFailures); err != nil || foreignKeyFailures != 0 {
		t.Fatalf("foreign key failures=%d err=%v", foreignKeyFailures, err)
	}
	repeat, err := store.SweepDataLifecycle(context.Background(), DefaultDataLifecyclePolicy())
	if err != nil || repeat.Changed() {
		t.Fatalf("retained-only repeat sweep=%#v err=%v", repeat, err)
	}
}

func TestDataLifecycleIndexesV74FailureRollsBackBothDDLAndJournal(t *testing.T) {
	store, _ := dataLifecycleIndexesV73Fixture(t)
	seedDataLifecycleIndexReferences(t, store)
	beforeRows := dataLifecycleIndexRows(t, store.db)
	// A conflicting object makes the second real DDL fail after the first runs.
	if _, err := store.db.Exec(`CREATE TABLE artifact_version_provenance_env_snapshot_idx (sentinel TEXT)`); err != nil {
		t.Fatal(err)
	}
	err := applyVersionedSchemaMigrations(context.Background(), store.db, time.Now)
	if err == nil || !strings.Contains(err.Error(), "migration 74 statement 2") {
		t.Fatalf("controlled second-index failure=%v", err)
	}
	var indexes, version int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN
		('artifact_version_provenance_lineage_snapshot_idx','artifact_version_provenance_env_snapshot_idx')`).Scan(&indexes); err != nil || indexes != 0 {
		t.Fatalf("partial indexes=%d err=%v", indexes, err)
	}
	if err := store.db.QueryRow(`SELECT MAX(version) FROM workspace_schema_migrations`).Scan(&version); err != nil || version != 73 {
		t.Fatalf("failed migration journal=%d err=%v", version, err)
	}
	if dataLifecycleIndexRows(t, store.db) != beforeRows {
		t.Fatal("failed migration changed retained data")
	}
	if _, err := store.db.Exec(`DROP TABLE artifact_version_provenance_env_snapshot_idx`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrations(context.Background(), store.db, time.Now); err != nil {
		t.Fatalf("forward retry after controlled conflict: %v", err)
	}
	assertDataLifecycleSnapshotIndexes(t, store.db)
}

func TestDataLifecycleIndexesV74PreflightRejectsMissingOrNonTextHashColumns(t *testing.T) {
	for _, shape := range []string{
		`CREATE TABLE artifact_version_provenance (lineage_snapshot_hash TEXT)`,
		`CREATE TABLE artifact_version_provenance (lineage_snapshot_hash TEXT, env_snapshot_hash INTEGER)`,
	} {
		t.Run(shape, func(t *testing.T) {
			db, err := sql.Open(sqliteDriver, filepath.Join(t.TempDir(), "shape.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(shape); err != nil {
				t.Fatal(err)
			}
			if err := runSchemaMigrationPreflight(context.Background(), db, dataLifecycleIndexesV74Migration); err == nil {
				t.Fatal("preflight accepted noncanonical snapshot hash columns")
			}
			var indexes int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index'`).Scan(&indexes); err != nil || indexes != 0 {
				t.Fatalf("preflight wrote indexes=%d err=%v", indexes, err)
			}
		})
	}
}

func TestDataLifecycleIndexesV74HasCanonicalIdentityAndRejectsJournalTampering(t *testing.T) {
	expected, err := ExpectedSchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	last := expected.Migrations[dataLifecycleIndexesV74Migration.version-1]
	const checksum = "09583c0ce841f1c5efa7ee0e2336a6478491fde3dbe322e581e6124679097239"
	if last.Version != 74 || last.Name != "data-lifecycle-snapshot-reference-indexes" || last.Checksum != checksum {
		t.Fatalf("migration identity=%#v", last)
	}
	store, _ := dataLifecycleIndexesV73Fixture(t)
	if err := applyVersionedSchemaMigrations(context.Background(), store.db, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE workspace_schema_migrations SET checksum='tampered' WHERE version=74`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrations(context.Background(), store.db, time.Now); err == nil || !strings.Contains(err.Error(), "migration 74 identity mismatch") {
		t.Fatalf("tampered checksum accepted: %v", err)
	}
}

func assertDataLifecycleSnapshotIndexes(t *testing.T, db *sql.DB) {
	t.Helper()
	for name, column := range map[string]string{
		"artifact_version_provenance_lineage_snapshot_idx": "lineage_snapshot_hash",
		"artifact_version_provenance_env_snapshot_idx":     "env_snapshot_hash",
	} {
		var unique, partial int
		var origin string
		if err := db.QueryRow(`SELECT "unique", partial, origin FROM pragma_index_list('artifact_version_provenance') WHERE name=?`, name).Scan(&unique, &partial, &origin); err != nil || unique != 0 || partial != 0 || origin != "c" {
			t.Fatalf("index %s unique=%d partial=%d origin=%s err=%v", name, unique, partial, origin, err)
		}
		var count int
		var actualColumn string
		if err := db.QueryRow(`SELECT COUNT(*), MAX(name) FROM pragma_index_info(?)`, name).Scan(&count, &actualColumn); err != nil || count != 1 || actualColumn != column {
			t.Fatalf("index %s columns=%d column=%s err=%v", name, count, actualColumn, err)
		}
	}
}

func dataLifecycleSnapshotDeletePlan(t *testing.T, db *sql.DB) string {
	t.Helper()
	// Match the unchanged production content-snapshot DELETE, including its OR.
	rows, err := db.Query(`EXPLAIN QUERY PLAN DELETE FROM content_snapshots
		WHERE created_at<? AND NOT EXISTS (
			SELECT 1 FROM artifact_version_provenance AS provenance
			WHERE provenance.lineage_snapshot_hash=content_snapshots.hash
				OR provenance.env_snapshot_hash=content_snapshots.hash
		)`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	t.Logf("content snapshot DELETE plan:\n%s", plan)
	return plan
}

func assertDataLifecycleIndexedPlan(t *testing.T, db *sql.DB) {
	t.Helper()
	plan := dataLifecycleSnapshotDeletePlan(t, db)
	for _, want := range []string{
		"MULTI-INDEX OR",
		"USING INDEX artifact_version_provenance_lineage_snapshot_idx (lineage_snapshot_hash=?)",
		"USING INDEX artifact_version_provenance_env_snapshot_idx (env_snapshot_hash=?)",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("snapshot DELETE plan missing %q: %s", want, plan)
		}
	}
	if strings.Contains(plan, "SCAN provenance") {
		t.Fatalf("snapshot DELETE still scans provenance: %s", plan)
	}
}

func seedDataLifecycleIndexReferences(t *testing.T, store *Store) time.Time {
	t.Helper()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	store.now = func() time.Time { return old }
	if _, err := store.CreateProject(CreateProjectInput{ID: "index-project", UserID: "owner", Name: "Index fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "active-root", ProjectID: "index-project", AgentName: "OPERON", Status: FrameStatusProcessing, ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []struct {
		hash string
		at   time.Time
	}{
		{"lineage-only", old}, {"env-only", old}, {"shared", old}, {"", old},
		{"orphan-old", old}, {"recent", now.Add(-30 * time.Minute)},
		{"at-cutoff", now.Add(-DefaultDataLifecyclePolicy().ContentSnapshotGrace)},
	} {
		if _, err := store.db.Exec(`INSERT INTO content_snapshots(hash,content,size_bytes,created_at) VALUES(?,'kept',4,?)`, snapshot.hash, snapshot.at); err != nil {
			t.Fatal(err)
		}
	}
	for index, reference := range [][2]any{
		{"lineage-only", "shared"}, {"shared", "env-only"}, {"shared", "shared"}, {nil, nil}, {"", nil},
	} {
		_, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
			ArtifactID: "retained-final", ProjectID: "index-project", Name: "final.txt", Kind: "text",
			Content: []byte(fmt.Sprintf("retained version %d", index)), CreatedBy: "active-root",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO artifact_version_provenance(version_id,content_type,lineage_snapshot_hash,env_snapshot_hash)
			VALUES(?,'text/plain',?,?)`, version.ID, reference[0], reference[1]); err != nil {
			t.Fatal(err)
		}
	}
	return now
}

func dataLifecycleIndexRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	var tables [][][]any
	for _, query := range []string{
		`SELECT * FROM projects ORDER BY id`, `SELECT * FROM frames ORDER BY id`,
		`SELECT * FROM artifacts ORDER BY id`, `SELECT * FROM artifact_versions ORDER BY id`,
		`SELECT * FROM artifact_version_provenance ORDER BY version_id`, `SELECT * FROM content_snapshots ORDER BY hash`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		var values [][]any
		for rows.Next() {
			row := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, values)
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
