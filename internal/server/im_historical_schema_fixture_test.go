package server

import (
	"database/sql"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Historical76 fixtures start from an empty reviewed86 database. Remove the
// reading-state86/Library85/collections84/Memory82 cadence/Memory81/Library80 objects and restore the Web definition
// changed by79 before removing newer migration receipts; otherwise the fixture
// falsely claims76 while retaining newer objects or the14-only CHECK.
// This helper is test-only and never touches an installed/user database.
func restoreHistoricalIMV76EmptyProjectionSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var facts int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM transcript_streams)+
		(SELECT COUNT(*) FROM transcript_web_projection_state)+(SELECT COUNT(*) FROM transcript_web_messages)+
		(SELECT COUNT(*) FROM transcript_web_message_identities)+(SELECT COUNT(*) FROM transcript_web_message_artifact_refs)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("historical projection reconstruction requires an empty private fixture: facts=%d err=%v", facts, err)
	}
	var currentDDL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='transcript_web_projection_state'`).Scan(&currentDDL); err != nil ||
		strings.TrimSpace(currentDDL) != strings.TrimSpace(transcriptstore.WebReadModelV79ProjectionStateStatement()) {
		t.Fatalf("historical reconstruction found an unexpected current projection definition: err=%v", err)
	}
	names, statements := transcriptstore.WebReadModelV38ObjectNames(), transcriptstore.WebReadModelV38CanonicalStatements()
	if len(names) != len(statements) {
		t.Fatal("canonical Transcript Web schema is inconsistent")
	}
	var readyIndex string
	for index, name := range names {
		if name == "transcript_web_projection_ready" {
			readyIndex = statements[index]
		}
	}
	if readyIndex == "" {
		t.Fatal("canonical Transcript Web ready index is missing")
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	dropEmptyReviewedMemoryAndLiteratureFixtureObjects(t, tx)
	for _, statement := range []string{
		`DROP TABLE transcript_web_projection_state`, transcriptstore.WebReadModelV74ProjectionStateStatement(), readyIndex,
	} {
		if _, err := tx.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
