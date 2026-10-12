package transcript

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// These literal domains document genuine persisted historical commitments.
// Only a sealed pre-upgrade source row can use them; current writes never do.
const historicalHistoryRunDomain = "synon.transcript.history-run.v1"
const historicalHistoryCandidateDomain = "synon.transcript.history-candidate.v1"

func namespaceHistoryAuditDigest(audit AskUserHistoryAudit) string {
	audit.ClassifiedAt = audit.ClassifiedAt.UTC()
	audit.Comparisons = append([]AskUserHistoryShadowComparison(nil), audit.Comparisons...)
	for index := range audit.Comparisons {
		audit.Comparisons[index].ComparedAt = audit.Comparisons[index].ComparedAt.UTC()
	}
	encoded, _ := json.Marshal(audit)
	digest := sha256.Sum256(append([]byte("x-science.namespace-original-history-audit.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:])
}

func validateHistoricalNamespaceHistoryAudit(ctx context.Context, query askUserHistoryQueryer, audit AskUserHistoryAudit, source []byte, capturing bool) error {
	if err := validateLoadedAskUserHistoryAuditWithDomains(audit, source, historicalHistoryRunDomain, historicalHistoryCandidateDomain); err != nil {
		return err
	}
	if capturing {
		return nil
	}
	var exists bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='namespace_history_audit_receipts')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrEventConflict
	}
	var original string
	runID, _ := hex.DecodeString(audit.RunID)
	err := query.QueryRowContext(ctx, `SELECT source_sha256 FROM namespace_history_audit_receipts WHERE run_id=? AND stream_uid=? AND owner_id=?`, runID, audit.StreamUID, audit.OwnerID).Scan(&original)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && original != namespaceHistoryAuditDigest(audit)) {
		return ErrEventConflict
	}
	return err
}

// CaptureNamespaceHistoryAuditReceipts is exclusively the transactional v74
// migration callback. It freezes compact source hashes, not history JSON, and
// validates every original run/candidate commitment before sealing the table.
func CaptureNamespaceHistoryAuditReceipts(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT run.run_id,run.stream_uid,stream.owner_id FROM transcript_history_classification_runs run JOIN transcript_streams stream ON stream.stream_uid=run.stream_uid ORDER BY run.run_id`)
	if err != nil {
		return err
	}
	var inputs []GetAskUserHistoryAuditInput
	for rows.Next() {
		var raw []byte
		var input GetAskUserHistoryAuditInput
		if err := rows.Scan(&raw, &input.StreamUID, &input.OwnerID); err != nil {
			_ = rows.Close()
			return err
		}
		input.RunID = hex.EncodeToString(raw)
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, input := range inputs {
		audit, found, err := loadAskUserHistoryAuditWithNamespace(ctx, tx, input, true)
		if err != nil {
			return err
		}
		if !found {
			return ErrEventConflict
		}
		runID, _ := hex.DecodeString(input.RunID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_history_audit_receipts(run_id,stream_uid,owner_id,source_sha256) VALUES(?,?,?,?)`, runID, input.StreamUID, input.OwnerID, namespaceHistoryAuditDigest(audit)); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TRIGGER namespace_history_audit_receipts_insert_guard BEFORE INSERT ON namespace_history_audit_receipts BEGIN SELECT RAISE(ABORT,'namespace history inventory is sealed'); END`,
		`CREATE TRIGGER namespace_history_audit_receipts_update_guard BEFORE UPDATE ON namespace_history_audit_receipts BEGIN SELECT RAISE(ABORT,'namespace history inventory is immutable'); END`,
		`CREATE TRIGGER namespace_history_audit_receipts_delete_guard BEFORE DELETE ON namespace_history_audit_receipts BEGIN SELECT RAISE(ABORT,'namespace history inventory is immutable'); END`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
