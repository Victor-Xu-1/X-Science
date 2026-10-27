package workspace

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) SetLiteratureCollectionItem(ctx context.Context, owner, collectionID, itemID, operationID string, included bool) (LiteratureCollectionReceipt, error) {
	if !validLiteratureID(collectionID) || !validLiteratureID(itemID) {
		return LiteratureCollectionReceipt{}, ErrLiteratureInvalid
	}
	request := struct {
		CollectionID, ItemID string
		Included             bool
	}{collectionID, itemID, included}
	return s.mutateLiteratureCollection(ctx, owner, operationID, "literature.collection.membership", request,
		func(tx *sql.Tx) (LiteratureCollectionReceipt, error) {
			if _, err := readLiteratureCollection(ctx, tx, owner, collectionID); err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			var deleted sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT deleted_at FROM literature_items WHERE id=? AND owner_user_id=?`, itemID, owner).Scan(&deleted)
			if errors.Is(err, sql.ErrNoRows) || err == nil && included && deleted.Valid {
				return LiteratureCollectionReceipt{}, ErrLiteratureUnavailable
			}
			if err != nil {
				return LiteratureCollectionReceipt{}, err
			}
			if included {
				// Ignore the existing row: a duplicate inclusion preserves its
				// original position and never advances metadata revision.
				_, err = tx.ExecContext(ctx, `INSERT INTO literature_collection_items(collection_id,item_id,sort_order,added_at)
 SELECT ?,?,COALESCE(MAX(sort_order)+1,0),? FROM literature_collection_items WHERE collection_id=?
 ON CONFLICT(collection_id,item_id) DO NOTHING`, collectionID, itemID, s.now().UTC(), collectionID)
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM literature_collection_items WHERE collection_id=? AND item_id=?`, collectionID, itemID)
			}
			return LiteratureCollectionReceipt{Schema: literatureCollectionReceiptSchema, OperationID: operationID,
				Action: "membership", CollectionID: collectionID, ItemID: itemID, Included: &included}, err
		})
}
