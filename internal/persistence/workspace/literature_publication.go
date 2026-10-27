package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"x-science/internal/literaturediscovery"
)

const literaturePublicationOperation = "literature.publication-check"

func publicationRequestHash(scope literaturediscovery.PublicationScope) (string, error) {
	if !validLiteratureID(scope.OwnerUserID) || !validLiteratureID(scope.ItemID) || !validLiteratureID(scope.OperationID) || scope.ExpectedMetadataRevision < 1 {
		return "", ErrLiteratureInvalid
	}
	return mutationRequestHash([]any{scope.ItemID, scope.ExpectedMetadataRevision})
}

// Read the source identity through the same owner/revision boundary as edits.
// Publication evidence never rewrites editable metadata or increments it.
func readPublicationDOI(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner, item string, revision int64) (string, error) {
	var actual int64
	var raw string
	var deleted sql.NullString
	err := query.QueryRowContext(ctx, `SELECT metadata_revision,metadata_json,deleted_at FROM literature_items WHERE id=? AND owner_user_id=?`, item, owner).Scan(&actual, &raw, &deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted.Valid {
		return "", ErrLiteratureUnavailable
	}
	if err != nil {
		return "", err
	}
	if actual != revision {
		return "", ErrLiteratureConflict
	}
	metadata, err := decodeLiteratureMetadataObject(raw)
	if err != nil {
		return "", err
	}
	value, _ := metadata["doi"].(string)
	doi, _ := literaturediscovery.NormalizeDOI(value)
	return doi, nil
}

func readPublicationEvidence(ctx context.Context, tx *sql.Tx, item, doi string) (*literaturediscovery.PublicationSnapshot, literaturediscovery.PublicationIssue, error) {
	if doi == "" {
		return nil, literaturediscovery.PublicationDOIRequired, nil
	}
	var storedDOI, raw string
	err := tx.QueryRowContext(ctx, `SELECT source_doi,snapshot_json FROM literature_publication_checks WHERE item_id=?`, item).Scan(&storedDOI, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if storedDOI != doi {
		return nil, "", nil
	}
	snapshot, valid := literaturediscovery.ParsePublicationSnapshot([]byte(raw))
	if !valid || snapshot.DOI != doi {
		return nil, literaturediscovery.PublicationResponseInvalid, nil
	}
	return &snapshot, "", nil
}

func newPublicationResult(item string, revision int64, doi string) literaturediscovery.PublicationResult {
	return literaturediscovery.PublicationResult{Schema: literaturediscovery.PublicationResultSchema, ItemID: item, MetadataRevision: revision, DOI: doi}
}

func validatePublicationReplay(scope literaturediscovery.PublicationScope, receipt literaturediscovery.PublicationReceipt) error {
	result := receipt.Result
	if receipt.Schema != literaturediscovery.PublicationReceiptSchema || receipt.OperationID != scope.OperationID ||
		result.Schema != literaturediscovery.PublicationResultSchema || result.ItemID != scope.ItemID || result.MetadataRevision != scope.ExpectedMetadataRevision {
		return ErrLiteratureInvalid
	}
	if result.DOI == "" {
		if result.Issue != literaturediscovery.PublicationDOIRequired || result.Snapshot != nil {
			return ErrLiteratureInvalid
		}
		return nil
	}
	canonical, valid := literaturediscovery.NormalizeDOI(result.DOI)
	if !valid || canonical != result.DOI || result.Issue == literaturediscovery.PublicationDOIRequired ||
		(result.Issue != "" && !validPublicationIssue(result.Issue)) || (result.Issue == "" && result.Snapshot == nil) {
		return ErrLiteratureInvalid
	}
	if result.Snapshot != nil {
		snapshot, valid := literaturediscovery.FreezePublicationSnapshot(*result.Snapshot)
		if !valid || snapshot.DOI != result.DOI {
			return ErrLiteratureInvalid
		}
	}
	return nil
}

// GetPublicationCheck reads saved evidence only; opening a detail never starts a
// provider query. The read transaction binds metadata and evidence together.
func (s *Store) GetPublicationCheck(ctx context.Context, owner, item string, revision int64) (literaturediscovery.PublicationResult, error) {
	result := newPublicationResult(item, revision, "")
	if !validLiteratureID(owner) || !validLiteratureID(item) || revision < 1 {
		return result, ErrLiteratureInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result.DOI, err = readPublicationDOI(ctx, tx, owner, item, revision)
	if err != nil {
		return result, err
	}
	result.Snapshot, result.Issue, err = readPublicationEvidence(ctx, tx, item, result.DOI)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) PreparePublicationCheck(ctx context.Context, scope literaturediscovery.PublicationScope) (literaturediscovery.PublicationPreparation, error) {
	var prepared literaturediscovery.PublicationPreparation
	hash, err := publicationRequestHash(scope)
	if err != nil {
		return prepared, err
	}
	var receipt literaturediscovery.PublicationReceipt
	if found, err := s.lookupMutationResult(ctx, scope.OwnerUserID, scope.OperationID, literaturePublicationOperation, hash, &receipt); err != nil || found {
		if found && err == nil {
			err = validatePublicationReplay(scope, receipt)
			if err == nil {
				prepared.Replay = &receipt
			}
		}
		return prepared, err
	}
	prepared.DOI, err = readPublicationDOI(ctx, s.readDatabase(), scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision)
	return prepared, err
}

func validPublicationIssue(issue literaturediscovery.PublicationIssue) bool {
	switch issue {
	case literaturediscovery.PublicationDOIRequired, literaturediscovery.PublicationUnavailable, literaturediscovery.PublicationRateLimited,
		literaturediscovery.PublicationTimeout, literaturediscovery.PublicationResponseInvalid, literaturediscovery.PublicationNotFound:
		return true
	}
	return false
}

// The original-operation result and its provider evidence commit atomically.
// Failed checks preserve any previous evidence and its original timestamp.
func (s *Store) CommitPublicationCheck(ctx context.Context, scope literaturediscovery.PublicationScope, doi string, fresh *literaturediscovery.PublicationSnapshot, issue literaturediscovery.PublicationIssue) (literaturediscovery.PublicationReceipt, error) {
	var receipt literaturediscovery.PublicationReceipt
	hash, err := publicationRequestHash(scope)
	if err != nil {
		return receipt, err
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	var frozen *literaturediscovery.PublicationSnapshot
	if fresh != nil {
		value, valid := literaturediscovery.FreezePublicationSnapshot(*fresh)
		if !valid || value.DOI != doi || issue != "" {
			return receipt, ErrLiteratureInvalid
		}
		frozen = &value
	} else if !validPublicationIssue(issue) || (issue == literaturediscovery.PublicationDOIRequired) != (doi == "") {
		return receipt, ErrLiteratureInvalid
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, err := lookupMutationResultTx(ctx, tx, scope.OwnerUserID, scope.OperationID, literaturePublicationOperation, hash, &receipt)
		if err != nil {
			return err
		}
		if found {
			return validatePublicationReplay(scope, receipt)
		}
		currentDOI, err := readPublicationDOI(ctx, tx, scope.OwnerUserID, scope.ItemID, scope.ExpectedMetadataRevision)
		if err != nil {
			return err
		}
		if currentDOI != doi {
			return ErrLiteratureConflict
		}
		result := newPublicationResult(scope.ItemID, scope.ExpectedMetadataRevision, doi)
		if frozen != nil {
			raw, err := json.Marshal(frozen)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO literature_publication_checks(item_id,source_doi,snapshot_json) VALUES(?,?,?)
 ON CONFLICT(item_id) DO UPDATE SET source_doi=excluded.source_doi,snapshot_json=excluded.snapshot_json`, scope.ItemID, doi, string(raw))
			if err != nil {
				return err
			}
			result.Snapshot = frozen
		} else {
			result.Snapshot, _, err = readPublicationEvidence(ctx, tx, scope.ItemID, doi)
			if err != nil {
				return err
			}
			result.Issue = issue
		}
		receipt = literaturediscovery.PublicationReceipt{Schema: literaturediscovery.PublicationReceiptSchema, OperationID: scope.OperationID, Result: result}
		if err := ctx.Err(); err != nil {
			return err
		}
		return insertMutationResultTx(ctx, tx, scope.OwnerUserID, scope.OperationID, literaturePublicationOperation, hash, receipt, s.now().UTC())
	})
	return receipt, err
}
