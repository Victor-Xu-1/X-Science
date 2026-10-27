package workspace

import (
	"context"
	"database/sql"
	"errors"
)

const literatureCollectionSelect = `SELECT c.id,c.revision,c.name,c.description,c.parent_id,c.sort_order,
 (SELECT COUNT(*) FROM literature_collection_items m JOIN literature_items i ON i.id=m.item_id
  WHERE m.collection_id=c.id AND i.owner_user_id=c.owner_user_id AND i.deleted_at IS NULL)
 FROM literature_collections c`

func scanLiteratureCollection(row interface{ Scan(...any) error }) (LiteratureCollection, error) {
	var collection LiteratureCollection
	var parent sql.NullString
	err := row.Scan(&collection.ID, &collection.Revision, &collection.Name, &collection.Description,
		&parent, &collection.SortOrder, &collection.ItemCount)
	if parent.Valid {
		collection.ParentID = &parent.String
	}
	return collection, err
}

func readLiteratureCollection(ctx context.Context, query literatureItemQuery, owner, id string) (LiteratureCollection, error) {
	collection, err := scanLiteratureCollection(query.QueryRowContext(ctx,
		literatureCollectionSelect+` WHERE c.id=? AND c.owner_user_id=?`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return collection, ErrLiteratureUnavailable
	}
	return collection, err
}

func (s *Store) ListLiteratureCollections(ctx context.Context, owner string) (LiteratureCollectionPage, error) {
	page := LiteratureCollectionPage{Schema: "x-science.literature-collections.v1", Collections: []LiteratureCollection{}}
	if !validLiteratureID(owner) {
		return page, ErrLiteratureInvalid
	}
	rows, err := s.readDatabase().QueryContext(ctx, literatureCollectionSelect+
		` WHERE c.owner_user_id=? ORDER BY c.sort_order,c.name,c.id`, owner)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		collection, err := scanLiteratureCollection(rows)
		if err != nil {
			return page, err
		}
		page.Collections = append(page.Collections, collection)
		ids[collection.ID] = true
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	for _, collection := range page.Collections {
		if collection.ParentID != nil && (!ids[*collection.ParentID] || *collection.ParentID == collection.ID) {
			return page, errors.New("literature collection parent is outside its owned tree")
		}
	}
	return page, nil
}

func requireLiveLiteratureItem(ctx context.Context, query literatureItemQuery, owner, id string) error {
	var found int
	err := query.QueryRowContext(ctx, `SELECT 1 FROM literature_items WHERE id=? AND owner_user_id=? AND deleted_at IS NULL`, id, owner).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLiteratureUnavailable
	}
	return err
}

func (s *Store) GetLiteratureItemCollections(ctx context.Context, owner, itemID string) (LiteratureCollectionMembership, error) {
	result := LiteratureCollectionMembership{Schema: "x-science.literature-collection-membership.v1", ItemID: itemID, CollectionIDs: []string{}}
	if !validLiteratureID(owner) || !validLiteratureID(itemID) {
		return result, ErrLiteratureInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := requireLiveLiteratureItem(ctx, tx, owner, itemID); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id FROM literature_collections c
 JOIN literature_collection_items m ON m.collection_id=c.id WHERE c.owner_user_id=? AND m.item_id=? ORDER BY c.id`, owner, itemID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		result.CollectionIDs = append(result.CollectionIDs, id)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
