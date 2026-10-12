package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"testing"
)

func TestNamespaceHistoricalAuditRequiresSealedExactOriginalCommitment(t *testing.T) {
	repo, db, _ := newTranscriptRepository(t)
	ctx := context.Background()
	stream, _ := seedAskUserHistoryClassificationFrame(t, repo, db, "namespace-history", true)
	audit, _, err := repo.AuditAskUserHistory(ctx, auditAskUserHistoryInput(stream, ""))
	if err != nil {
		t.Fatal(err)
	}
	var source [sha256.Size]byte
	raw, _ := hex.DecodeString(audit.SourceSHA256)
	copy(source[:], raw)
	canonicalRunID, _ := hex.DecodeString(audit.RunID)
	if _, err := db.Exec(`DELETE FROM transcript_history_classification_runs WHERE run_id=?`, canonicalRunID); err != nil {
		t.Fatal(err)
	}
	audit.RunID = askUserHistoryRunIDWithDomain(audit, source, historicalHistoryRunDomain)
	for index := range audit.Details {
		audit.Details[index].CandidateID = askUserHistoryCandidateIDWithDomain(audit.RunID, audit.Details[index], historicalHistoryCandidateDomain)
	}
	// Build a historical contract fixture through the real strict persistence
	// writer. No production schema/data or guarded receipt inventory is changed.
	if err := repo.withImmediate(ctx, func(conn *sql.Conn) error { return insertAskUserHistoryAuditConn(ctx, conn, audit) }); err != nil {
		t.Fatal(err)
	}
	input := GetAskUserHistoryAuditInput{RunID: audit.RunID, StreamUID: stream.UID, OwnerID: stream.OwnerID}
	if _, _, err := repo.GetAskUserHistoryAudit(ctx, input); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("unproven historical audit admitted: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE namespace_history_audit_receipts(run_id BLOB PRIMARY KEY NOT NULL,stream_uid TEXT NOT NULL,owner_id TEXT NOT NULL,source_sha256 TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := CaptureNamespaceHistoryAuditReceipts(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, found, err := repo.GetAskUserHistoryAudit(ctx, input)
	if err != nil || !found || stored.RunID != audit.RunID || stored.SourceSHA256 != audit.SourceSHA256 {
		t.Fatalf("stored=%#v found=%v err=%v", stored, found, err)
	}
	foreign := input
	foreign.OwnerID = "foreign"
	if _, _, err := repo.GetAskUserHistoryAudit(ctx, foreign); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("foreign=%v", err)
	}
	if _, err := db.Exec(`INSERT INTO namespace_history_audit_receipts VALUES(?,?,?,?)`, raw, stream.UID, stream.OwnerID, "forged"); err == nil {
		t.Fatal("sealed inventory accepted a late source")
	}
	runID, _ := hex.DecodeString(audit.RunID)
	if _, err := db.Exec(`UPDATE transcript_history_shadow_comparisons SET evidence_json='{"changed":true}' WHERE run_id=? AND dimension='stable_ids'`, runID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.GetAskUserHistoryAudit(ctx, input); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("changed historical shape admitted: %v", err)
	}
}
