package workspace

import (
	"context"
	"testing"
	"time"
)

func TestModelProviderContextV71UnknownBackfillAndTransactionalRecovery(t *testing.T) {
	db := newTranscriptWebV69Database(t)
	ctx := context.Background()
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 70); err != nil {
		t.Fatal(err)
	}
	insertProviderV22Fixture(t, db, "context-legacy", "custom", "https://provider.invalid/v1", "secret-ref")
	if _, err := db.Exec(`UPDATE model_providers SET max_tokens=2048 WHERE id='context-legacy'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_v71_journal BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=71 BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 71); err == nil {
		t.Fatal("migration journal failure ignored")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('model_providers') WHERE name='context_window'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration left column: %d %v", count, err)
	}
	assertSchemaJournalVersion(t, db, 70)
	if _, err := db.Exec(`DROP TRIGGER fail_v71_journal`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 71); err != nil {
			t.Fatal(err)
		}
	}
	var window *int
	var output int
	if err := db.QueryRow(`SELECT context_window,max_tokens FROM model_providers WHERE id='context-legacy'`).Scan(&window, &output); err != nil || window != nil || output != 2048 {
		t.Fatalf("legacy capacity invented or output changed: %v/%d %v", window, output, err)
	}
	for _, value := range []any{0, -1, 10000001, 128000.5, "unknown"} {
		if _, err := db.Exec(`UPDATE model_providers SET context_window=? WHERE id='context-legacy'`, value); err == nil {
			t.Fatalf("invalid database capacity accepted: %v", value)
		}
	}
	assertSchemaJournalVersion(t, db, 71)
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("database integrity: %s %v", integrity, err)
	}
}
