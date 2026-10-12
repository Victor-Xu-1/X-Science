package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var namespaceReceiptQueries = map[string]string{
	"frame":             "SELECT EXISTS(SELECT 1 FROM frames WHERE id=?)",
	"transcript-stream": "SELECT EXISTS(SELECT 1 FROM transcript_streams WHERE stream_uid=?)",
	"outbox":            "SELECT EXISTS(SELECT 1 FROM workspace_outbox WHERE event_id=?)",
	"outbox-realtime":   "SELECT EXISTS(SELECT 1 FROM workspace_outbox WHERE topic='workspace.realtime' AND json_valid(payload_json) AND json_extract(payload_json,'$.event.id')=?)",
	"frame-event":       "SELECT EXISTS(SELECT 1 FROM frame_events WHERE id=?)",
	"realtime-event":    "SELECT EXISTS(SELECT 1 FROM realtime_events WHERE id=?)",
	"kernel-operation":  "SELECT EXISTS(SELECT 1 FROM kernel_local_operations WHERE operation_id=?)",
	"tool-batch":        "SELECT EXISTS(SELECT 1 FROM transcript_tool_call_batches WHERE batch_id=?)",
	"artifact":          "SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=?)",
	"artifact-version":  "SELECT EXISTS(SELECT 1 FROM artifact_versions WHERE id=?)",
	"compute-job":       "SELECT EXISTS(SELECT 1 FROM compute_workbench_jobs WHERE job_id=?)",
}

// ResolveNamespaceIdentityTx is a receipt lookup, never a legacy write path.
// Only identities frozen at the v74 cutover can acquire an alias. The caller
// must validate its usual owner, immutable input, and terminal/CAS contracts in
// the same transaction before committing an effect or returning its receipt.
func (s *Store) ResolveNamespaceIdentityTx(ctx context.Context, tx workspaceTransaction, kind, canonical, historical string) (string, error) {
	if s == nil || tx == nil {
		return "", errors.New("namespace identity transaction is required")
	}
	query, allowed := namespaceReceiptQueries[kind]
	if !allowed || !namespaceIdentityValid(canonical) || (historical != "" && !namespaceIdentityValid(historical)) {
		return "", errors.New("namespace identity binding is invalid")
	}
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='namespace_identity_aliases')`).Scan(&ready); err != nil {
		return "", err
	}
	if !ready {
		// Prior-version migration callbacks may exercise canonical mutations
		// before v74 exists. An already-cut-over journal cannot lose its guard.
		var version int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM workspace_schema_migrations`).Scan(&version); err != nil {
			return "", err
		}
		if version >= 74 {
			return "", errors.New("namespace cutover receipt guard is missing")
		}
		return canonical, nil
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT existing_id FROM namespace_identity_aliases WHERE kind=? AND canonical_id=?`, kind, canonical).Scan(&existing)
	if err == nil {
		if historical != "" && historical != existing {
			return "", errors.New("namespace identity alias conflicts with requested receipt")
		}
		if err := requireNamespaceReceipt(ctx, tx, query, existing); err != nil {
			return "", err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if historical == "" || historical == canonical {
		return canonical, nil
	}
	var frozen bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM namespace_cutover_receipts WHERE kind=? AND existing_id=?)`, kind, historical).Scan(&frozen); err != nil {
		return "", err
	}
	if !frozen {
		return canonical, nil
	}
	if err := requireNamespaceReceipt(ctx, tx, query, historical); err != nil {
		return "", err
	}
	var occupied bool
	if err := tx.QueryRowContext(ctx, query, canonical).Scan(&occupied); err != nil {
		return "", err
	}
	if occupied {
		return "", errors.New("canonical and historical namespace receipts conflict")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO namespace_identity_aliases(kind,canonical_id,existing_id,created_at_ms) VALUES(?,?,?,?)`, kind, canonical, historical, s.now().UTC().UnixMilli()); err != nil {
		return "", fmt.Errorf("bind historical namespace receipt: %w", err)
	}
	return historical, nil
}

func namespaceIdentityValid(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

// HasNamespaceCutoverReceipt proves only historical presence. Callers must
// additionally resolve the actual receipt through its owner-scoped repository
// and validate the unchanged execution/environment identity before recovery.
func (s *Store) HasNamespaceCutoverReceipt(ctx context.Context, kind, identity string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("workspace store is closed")
	}
	query, allowed := namespaceReceiptQueries[kind]
	if !allowed || !namespaceIdentityValid(identity) {
		return false, errors.New("namespace cutover receipt identity is invalid")
	}
	var frozen, present bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM namespace_cutover_receipts WHERE kind=? AND existing_id=?)`, kind, identity).Scan(&frozen); err != nil {
		return false, err
	}
	if !frozen {
		return false, nil
	}
	if err := s.db.QueryRowContext(ctx, query, identity).Scan(&present); err != nil {
		return false, err
	}
	return present, nil
}

func requireNamespaceReceipt(ctx context.Context, tx workspaceTransaction, query, identity string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, query, identity).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("historical namespace receipt no longer exists; replay is not authorized")
	}
	return nil
}
