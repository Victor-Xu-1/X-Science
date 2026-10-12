package workspace

import (
	"context"
	"database/sql"
	"errors"
)

// This child receipt is recorded only after a verified controlled download and
// a successful full historical checkpoint read. It is evidence, not job state,
// execution permission, or a replacement for the immutable input authority.
type ComputeNamespaceObservedSourceReceipt struct {
	JobID, OwnerUserID, SourceKind, SourceSHA256, InputSHA256 string
	Environment, Provider, ExternalID, ManifestSHA256         string
	CreatedAtMS                                               int64
}

func computeObservedSourceMatches(receipt ComputeNamespaceObservedSourceReceipt, frozen ComputeNamespaceCutoverReceipt) bool {
	return receipt.JobID == frozen.JobID && receipt.OwnerUserID == frozen.OwnerUserID && receipt.SourceKind == "checkpoint" &&
		receipt.SourceSHA256 == frozen.SourceSHA256 && receipt.InputSHA256 == frozen.InputSHA256 && receipt.Environment == frozen.Environment &&
		receipt.Provider == frozen.Provider && receipt.ExternalID == frozen.ExternalID && validLowerHexSHA256(receipt.ManifestSHA256) &&
		validLowerHexSHA256(receipt.InputSHA256) && validLowerHexSHA256(receipt.SourceSHA256)
}

func scanComputeObservedSource(row interface{ Scan(...any) error }) (ComputeNamespaceObservedSourceReceipt, error) {
	var receipt ComputeNamespaceObservedSourceReceipt
	err := row.Scan(&receipt.JobID, &receipt.OwnerUserID, &receipt.SourceKind, &receipt.SourceSHA256, &receipt.InputSHA256, &receipt.Environment, &receipt.Provider, &receipt.ExternalID, &receipt.ManifestSHA256, &receipt.CreatedAtMS)
	return receipt, err
}

const computeObservedSourceColumns = `job_id,owner_user_id,source_kind,source_sha256,input_sha256,environment,provider,external_id,manifest_sha256,created_at_ms`

func (s *Store) RecordComputeNamespaceObservedSource(ctx context.Context, ownerID string, input ComputeNamespaceObservedSourceReceipt) (ComputeNamespaceObservedSourceReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return ComputeNamespaceObservedSourceReceipt{}, errors.New("workspace store is closed")
	}
	if !namespaceIdentityValid(ownerID) || !namespaceIdentityValid(input.JobID) || input.OwnerUserID != ownerID || input.SourceKind != "checkpoint" || !validLowerHexSHA256(input.ManifestSHA256) {
		return ComputeNamespaceObservedSourceReceipt{}, errors.New("compute observed source authority is invalid")
	}
	var recorded ComputeNamespaceObservedSourceReceipt
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		frozen, found, err := getComputeNamespaceCutoverReceiptQuery(ctx, tx, ownerID, input.JobID)
		if err != nil {
			return err
		}
		if !found || !computeObservedSourceMatches(input, frozen) {
			return errors.New("compute observed source does not match admitted historical job")
		}
		existing, err := scanComputeObservedSource(tx.QueryRowContext(ctx, `SELECT `+computeObservedSourceColumns+` FROM compute_namespace_observed_source_receipts WHERE job_id=? AND source_kind=? AND manifest_sha256=?`, input.JobID, input.SourceKind, input.ManifestSHA256))
		if err == nil {
			if !computeObservedSourceMatches(existing, frozen) || existing.OwnerUserID != ownerID {
				return errors.New("compute observed source receipt conflicts with original binding")
			}
			recorded = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		input.CreatedAtMS = s.now().UTC().UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO compute_namespace_observed_source_receipts(`+computeObservedSourceColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?)`, input.JobID, input.OwnerUserID, input.SourceKind, input.SourceSHA256, input.InputSHA256, input.Environment, input.Provider, input.ExternalID, input.ManifestSHA256, input.CreatedAtMS); err != nil {
			return err
		}
		recorded = input
		return nil
	})
	return recorded, err
}

func (s *Store) GetComputeNamespaceObservedSource(ctx context.Context, ownerID, jobID, kind, manifestSHA256 string) (ComputeNamespaceObservedSourceReceipt, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return ComputeNamespaceObservedSourceReceipt{}, false, errors.New("workspace store is closed")
	}
	if !namespaceIdentityValid(ownerID) || !namespaceIdentityValid(jobID) || kind != "checkpoint" || !validLowerHexSHA256(manifestSHA256) {
		return ComputeNamespaceObservedSourceReceipt{}, false, errors.New("compute observed source lookup authority is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ComputeNamespaceObservedSourceReceipt{}, false, err
	}
	defer tx.Rollback()
	frozen, found, err := getComputeNamespaceCutoverReceiptQuery(ctx, tx, ownerID, jobID)
	if err != nil || !found {
		return ComputeNamespaceObservedSourceReceipt{}, false, err
	}
	receipt, err := scanComputeObservedSource(tx.QueryRowContext(ctx, `SELECT `+computeObservedSourceColumns+` FROM compute_namespace_observed_source_receipts WHERE job_id=? AND owner_user_id=? AND source_kind=? AND manifest_sha256=?`, jobID, ownerID, kind, manifestSHA256))
	if errors.Is(err, sql.ErrNoRows) {
		return ComputeNamespaceObservedSourceReceipt{}, false, nil
	}
	if err != nil {
		return ComputeNamespaceObservedSourceReceipt{}, false, err
	}
	if !computeObservedSourceMatches(receipt, frozen) {
		return ComputeNamespaceObservedSourceReceipt{}, false, errors.New("compute observed source original binding changed")
	}
	if err := tx.Commit(); err != nil {
		return ComputeNamespaceObservedSourceReceipt{}, false, err
	}
	return receipt, true, nil
}
