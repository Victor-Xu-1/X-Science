package workspace

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResourceTagsV90PreservesPrefixAndRollsBackDDL(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 89); err != nil {
		t.Fatal(err)
	}
	seedLiteratureIdentityV83LegacyFixture(t, db)
	before := transcriptWebMigrationRows(t, db)
	journal := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	const targets = `SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('resource_tag_catalog_state','resource_tags','resource_tags_owner_order_idx','resource_tag_assignments','resource_tag_assignments_resource_idx','resource_tags_literature_delete')`
	assertRetained := func() {
		t.Helper()
		after := transcriptWebMigrationRows(t, db)
		for name, rows := range before {
			if !reflect.DeepEqual(rows, after[name]) {
				t.Fatalf("migration changed old rows in %s", name)
			}
		}
		got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
		if len(got) < 89 || !reflect.DeepEqual(journal, got[:89]) {
			t.Fatal("old migration journal changed")
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_resource_tags_marker BEFORE INSERT ON workspace_schema_migrations WHEN NEW.version=90 BEGIN SELECT RAISE(ABORT,'injected tag marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 90); err == nil || !strings.Contains(err.Error(), "injected tag marker failure") {
		t.Fatalf("expected actual marker failure: %v", err)
	}
	assertRetained()
	var count int
	if err := db.QueryRow(targets).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration left DDL: %d %v", count, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_resource_tags_marker`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 90); err != nil {
		t.Fatal(err)
	}
	assertRetained()
	if err := db.QueryRow(targets).Scan(&count); err != nil || count != 6 {
		t.Fatalf("new DDL: %d %v", count, err)
	}
	committed := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, func() time.Time { return time.Now().Add(time.Hour) }, 90); err != nil {
		t.Fatal(err)
	}
	if got := literatureIdentityV83Values(t, db, literatureIdentityV83JournalQuery); !reflect.DeepEqual(committed, got) {
		t.Fatal("repeat migration changed the committed journal")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s %v", integrity, err)
	}
}

func TestResourceTagsV90RejectsUnreviewedTargetAndOwnerCrossLink(t *testing.T) {
	db, _ := newTranscriptWebV78Database(t)
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 89); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE resource_tags(unreviewed TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 90); err == nil {
		t.Fatal("unreviewed existing target accepted")
	}
	if _, err := db.Exec(`DROP TABLE resource_tags`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(t.Context(), db, time.Now, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO resource_tag_catalog_state(owner_user_id) VALUES('alice'),('bob')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO resource_tags(owner_user_id,id,sort_order,created_at,updated_at) VALUES('alice','tag-favorite',0,1,1)`); err == nil {
		t.Fatal("NULL system identity bypassed the protected favorite shape")
	}
	if _, err := db.Exec(`INSERT INTO resource_tags(owner_user_id,id,system_key,sort_order,created_at,updated_at) VALUES('alice','tag-favorite','favorite',0,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO resource_tag_assignments VALUES('bob','tag-favorite','literature.item','item',1)`); err == nil {
		t.Fatal("cross-account tag relationship bypassed composite foreign key")
	}
	if _, err := db.Exec(`INSERT INTO resource_tags(owner_user_id,id,system_key,sort_order,created_at,updated_at) VALUES('bob','tag-favorite','favorite',0,1,1)`); err != nil {
		t.Fatal("same system identity cannot be used by an independent account:", err)
	}
}
