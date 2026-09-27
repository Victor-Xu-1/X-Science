package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestTranscriptWebProjectorV70PreservesRowsAndRequeuesV11(t *testing.T) {
	db := newTranscriptWebV69Database(t)
	before := transcriptWebMigrationRows(t, db)
	ctx := context.Background()
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 70); err != nil {
		t.Fatal(err)
	}
	assertTranscriptWebProjectorVersionConstraint(t, db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11,12))")
	if after := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed persisted source or derived rows")
	}
	readModel := transcriptstore.NewWebReadModelRepository(db, db)
	work, err := readModel.ListTranscriptWebProjectionWork(ctx, "owner-v70", 10)
	if err != nil || len(work) != 1 || work[0].Reason != "projection_projector_version_stale" {
		t.Fatalf("v11 quarantine was not scheduled for rebuild: work=%#v err=%v", work, err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 70); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if after := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("repeated migration changed persisted rows")
	}
	if _, err := db.Exec(`UPDATE transcript_web_projection_state SET projector_version=12`); err != nil {
		t.Fatalf("current projector version rejected: %v", err)
	}
	if _, err := db.Exec(`UPDATE transcript_web_projection_state SET projector_version=13`); err == nil {
		t.Fatal("unsupported projector version accepted")
	}
	assertTranscriptWebV70Integrity(t, db, 70)
}

func TestTranscriptWebProjectorV70FailureRollsBackSchemaAndRows(t *testing.T) {
	db := newTranscriptWebV69Database(t)
	before := transcriptWebMigrationRows(t, db)
	if _, err := db.Exec(`CREATE TRIGGER fail_v70_journal BEFORE INSERT ON workspace_schema_migrations
		WHEN NEW.version=70 BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 70); err == nil {
		t.Fatal("injected journal failure ignored")
	}
	assertTranscriptWebProjectorVersionConstraint(t, db, "CHECK(projector_versionIN(1,2,3,4,5,6,7,8,9,10,11))")
	if after := transcriptWebMigrationRows(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed persisted rows")
	}
	assertTranscriptWebV70Integrity(t, db, 69)
	if _, err := db.Exec(`DROP TRIGGER fail_v70_journal`); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 70); err != nil {
		t.Fatalf("migration could not recover after rollback: %v", err)
	}
	assertTranscriptWebV70Integrity(t, db, 70)
}

func newTranscriptWebV69Database(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	store := &Store{db: db, now: time.Now, blobRoot: path + ".blobs"}
	if err := prepareSchemaJournal(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := store.prepareLegacyWorkspaceSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := applyVersionedSchemaMigrationsThrough(ctx, db, time.Now, 69); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(CreateProjectInput{ID: "project-v70", UserID: "owner-v70", Name: "v70"})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := store.CreateFrame(CreateFrameInput{
		ID: "frame-v70", ProjectID: project.ID, AgentName: "agent", Status: "pending", ConversationType: "execution",
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := transcriptstore.NewRepository(db)
	stream, err := repo.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: "stream-v70", OwnerID: "owner-v70", ExternalID: frame.ID,
		SessionID: frame.ID, Kind: transcriptstore.StreamKindFrameRef, Epoch: 1,
		ProjectID: project.ID, FrameID: frame.ID, RootFrameID: frame.RootFrameID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "client-v70",
		FrameEventID: "event-v70", MessageUUID: "message-v70", Text: "preserve this source",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO transcript_web_projection_state(
		stream_uid,branch_id,branch_generation,projector_version,projection_revision,
		through_publication_seq,source_revision,message_count,visible_message_count,
		message_artifact_reference_count,projector_state_json,projector_state_sha256,
		source_chain_sha256,status,last_error_code,updated_at
	) VALUES(?,?,?,11,1,?,0,1,1,0,'{}',?,?,'quarantined','projection_source_conflict',CURRENT_TIMESTAMP)`,
		stream.UID, snapshot.BranchID, snapshot.BranchGeneration, snapshot.ThroughPublicationSequence, digest, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_web_messages(
		stream_uid,branch_id,ordinal,message_id,client_message_id,visible,visible_index,
		message_json,message_sha256,first_publication_seq,last_publication_seq,updated_at
	) VALUES(?,?,1,'message-v70','client-v70',1,0,'{}',?,1,1,CURRENT_TIMESTAMP)`,
		stream.UID, snapshot.BranchID, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_web_message_identities(
		stream_uid,branch_id,identity,message_ordinal,kind
	) VALUES(?,?,'message-v70',1,'both')`, stream.UID, snapshot.BranchID); err != nil {
		t.Fatal(err)
	}
	return db
}

// Snapshot every application table except the migration journal: widening a
// derived-view constraint must not edit existing source or materialized rows.
func transcriptWebMigrationRows(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	names, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table'
		AND name NOT LIKE 'sqlite_%' AND name!='workspace_schema_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := names.Err(); err != nil {
		t.Fatal(err)
	}
	_ = names.Close()
	result := make(map[string][]string, len(tables))
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		result[table] = nil
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			result[table] = append(result[table], fmt.Sprintf("%#v", values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		sort.Strings(result[table])
	}
	return result
}

func assertTranscriptWebV70Integrity(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	for query, want := range map[string]int{
		`SELECT MAX(version) FROM workspace_schema_migrations`: version,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`:        0,
		`PRAGMA foreign_keys`:                                  1, `PRAGMA legacy_alter_table`: 0,
		`SELECT COUNT(*) FROM sqlite_schema WHERE name GLOB 'transcript_web_*_v69'`: 0,
	} {
		var got int
		if err := db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("integrity check %q: got=%d want=%d err=%v", query, got, want, err)
		}
	}
}
