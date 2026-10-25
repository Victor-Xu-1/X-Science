package workspace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

const literatureCollectionReceiptSchema = "x-science.literature-collection-receipt.v1"

// The generic mutation ledger remains the only idempotency authority. In
// particular the exact original receipt precedes current parent/name checks.
func (s *Store) mutateLiteratureCollection(ctx context.Context, owner, operationID, operation string, request any,
	apply func(*sql.Tx) (LiteratureCollectionReceipt, error),
) (LiteratureCollectionReceipt, error) {
	var receipt LiteratureCollectionReceipt
	if !validLiteratureID(owner) || !validLiteratureID(operationID) {
		return receipt, ErrLiteratureInvalid
	}
	hash, err := mutationRequestHash(request)
	if err != nil {
		return receipt, err
	}
	if found, err := s.lookupMutationResult(ctx, owner, operationID, operation, hash, &receipt); err != nil || found {
		return receipt, err
	}
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		found, err := lookupMutationResultTx(ctx, tx, owner, operationID, operation, hash, &receipt)
		if err != nil || found {
			return err
		}
		receipt, err = apply(tx)
		if err != nil {
			return err
		}
		return insertMutationResultTx(ctx, tx, owner, operationID, operation, hash, receipt, s.now().UTC())
	})
	if err != nil {
		// SQLITE_CONSTRAINT_UNIQUE is the name-index backstop here. Primary
		// identity/ledger conflicts and FK failures retain their own errors.
		var coded interface{ Code() int }
		if errors.As(err, &coded) && coded.Code() == 2067 {
			err = ErrLiteratureCollectionNameConflict
		}
		return LiteratureCollectionReceipt{}, err
	}
	return receipt, nil
}

func (s *Store) CreateLiteratureCollection(ctx context.Context, owner string, input LiteratureCollectionCreate) (LiteratureCollectionReceipt, error) {
	if input.ParentID != "" && !validLiteratureID(input.ParentID) {
		return LiteratureCollectionReceipt{}, ErrLiteratureInvalid
	}
	return s.mutateLiteratureCollection(ctx, owner, input.OperationID, "literature.collection.create", input,
		func(tx *sql.Tx) (LiteratureCollectionReceipt, error) {
			name, key, description, err := literatureCollectionName(input.Name, input.Description)
			if err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			if input.ParentID != "" {
				if _, err := readLiteratureCollection(ctx, tx, owner, input.ParentID); err != nil {
					return LiteratureCollectionReceipt{}, err
				}
			}
			var order int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order)+1,0) FROM literature_collections
 WHERE owner_user_id=? AND parent_id IS ?`, owner, literatureCollectionParent(input.ParentID)).Scan(&order); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			id, now := uuid.NewString(), s.now().UTC()
			if _, err := tx.ExecContext(ctx, `INSERT INTO literature_collections
 (id,owner_user_id,name,name_key,description,parent_id,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
				id, owner, name, key, description, literatureCollectionParent(input.ParentID), order, now, now); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			collection, err := readLiteratureCollection(ctx, tx, owner, id)
			return LiteratureCollectionReceipt{Schema: literatureCollectionReceiptSchema, OperationID: input.OperationID,
				Action: "create", CollectionID: id, Collection: &collection}, err
		})
}

func (s *Store) UpdateLiteratureCollection(ctx context.Context, owner, id string, input LiteratureCollectionUpdate) (LiteratureCollectionReceipt, error) {
	if !validLiteratureID(id) || input.ExpectedRevision < 1 {
		return LiteratureCollectionReceipt{}, ErrLiteratureInvalid
	}
	request := struct {
		ID    string
		Input LiteratureCollectionUpdate
	}{id, input}
	return s.mutateLiteratureCollection(ctx, owner, input.OperationID, "literature.collection.update", request,
		func(tx *sql.Tx) (LiteratureCollectionReceipt, error) {
			previous, err := readLiteratureCollection(ctx, tx, owner, id)
			if err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			if previous.Revision != input.ExpectedRevision {
				return LiteratureCollectionReceipt{}, ErrLiteratureConflict
			}
			name, key, description, err := literatureCollectionName(input.Name, input.Description)
			if err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			changed, err := tx.ExecContext(ctx, `UPDATE literature_collections SET name=?,name_key=?,description=?,revision=revision+1,updated_at=?
 WHERE id=? AND owner_user_id=? AND revision=?`, name, key, description, s.now().UTC(), id, owner, input.ExpectedRevision)
			if err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			if count, err := changed.RowsAffected(); err != nil {
				return LiteratureCollectionReceipt{}, err
			} else if count != 1 {
				return LiteratureCollectionReceipt{}, ErrLiteratureConflict
			}
			collection, err := readLiteratureCollection(ctx, tx, owner, id)
			return LiteratureCollectionReceipt{Schema: literatureCollectionReceiptSchema, OperationID: input.OperationID,
				Action: "update", CollectionID: id, Collection: &collection}, err
		})
}

func (s *Store) DeleteLiteratureCollection(ctx context.Context, owner, id, operationID string) (LiteratureCollectionReceipt, error) {
	if !validLiteratureID(id) {
		return LiteratureCollectionReceipt{}, ErrLiteratureInvalid
	}
	return s.mutateLiteratureCollection(ctx, owner, operationID, "literature.collection.delete", id,
		func(tx *sql.Tx) (LiteratureCollectionReceipt, error) {
			if _, err := readLiteratureCollection(ctx, tx, owner, id); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE literature_collections SET revision=revision+1,updated_at=?
 WHERE parent_id=? AND owner_user_id=?`, s.now().UTC(), id, owner); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			// ON DELETE SET NULL promotes direct children. A root-name collision
			// rolls this deletion and every preceding revision update back.
			if _, err := tx.ExecContext(ctx, `DELETE FROM literature_collections WHERE id=? AND owner_user_id=?`, id, owner); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			return LiteratureCollectionReceipt{Schema: literatureCollectionReceiptSchema, OperationID: operationID,
				Action: "delete", CollectionID: id}, nil
		})
}
