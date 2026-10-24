package workspace

import (
	"context"
	"database/sql"
	"fmt"
)

// Reuse the existing row and transaction authority. Only Library import/Patch
// call this matcher; every generic mutation reader remains exact-only.
func lookupLiteratureMutationResultTx(ctx context.Context, tx *sql.Tx, owner, operationID, operation string, identity literatureRequestIdentity, receipt *LiteratureReceipt) (bool, error) {
	storedHash, raw, found, err := readMutationResultTx(ctx, tx, owner, operationID, operation)
	if err != nil || !found {
		return false, err
	}
	if !identity.matches(storedHash) {
		return false, ErrMutationIdempotencyConflict
	}
	if err := decodeMutationResult(raw, receipt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) lookupLiteratureMutationResult(ctx context.Context, owner, operationID, operation string, identity literatureRequestIdentity, receipt *LiteratureReceipt) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("begin mutation result lookup: %w", err)
	}
	defer tx.Rollback()
	found, err := lookupLiteratureMutationResultTx(ctx, tx, owner, operationID, operation, identity, receipt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("finish mutation result lookup: %w", err)
	}
	return found, nil
}
