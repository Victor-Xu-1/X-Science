package workspace

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
)

// Preserve the four original unscoped cursor fields and their encoding. Order
// is a position in a live collection, not a membership snapshot revision.
type literatureCursor struct {
	After, Query, Project, Owner string
	Collection                   string `json:",omitempty"`
	Order                        *int64 `json:",omitempty"`
}

func (s *Store) ListLiteratureItemsInCollection(ctx context.Context, owner, query, project, collectionID, cursor string, limit int) (LiteraturePage, error) {
	page := LiteraturePage{Schema: "x-science.literature-page.v1", Items: []LiteratureItem{}}
	if !validLiteratureID(owner) || len(query) > 512 || limit < 1 || limit > 100 || len(cursor) > 4096 ||
		collectionID != "" && !validLiteratureID(collectionID) {
		return page, ErrLiteratureInvalid
	}
	bound := literatureCursor{Query: query, Project: project, Owner: owner, Collection: collectionID}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var decoded literatureCursor
		if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.Query != query || decoded.Project != project ||
			decoded.Owner != owner || decoded.Collection != collectionID ||
			collectionID == "" && decoded.Order != nil || collectionID != "" && (decoded.Order == nil || *decoded.Order < 0) {
			return page, ErrLiteratureInvalid
		}
		bound = decoded
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if collectionID != "" {
		if _, err := readLiteratureCollection(ctx, tx, owner, collectionID); err != nil {
			return page, err
		}
	}
	const filter = ` AND (?='' OR instr(lower(i.title),lower(?))>0)
 AND (?='' OR EXISTS(SELECT 1 FROM project_literature p JOIN projects project ON project.id=p.project_id
 WHERE p.item_id=i.id AND p.project_id=? AND project.user_id=?))`
	statement := `SELECT i.id,0 FROM literature_items i WHERE i.owner_user_id=? AND i.deleted_at IS NULL AND i.id>?` +
		filter + ` ORDER BY i.id LIMIT ?`
	args := []any{owner, bound.After, query, query, project, project, owner, limit + 1}
	if collectionID != "" {
		order := int64(-1)
		if bound.Order != nil {
			order = *bound.Order
		}
		statement = `SELECT i.id,m.sort_order FROM literature_items i JOIN literature_collection_items m
 ON m.item_id=i.id AND m.collection_id=? WHERE i.owner_user_id=? AND i.deleted_at IS NULL
 AND (m.sort_order>? OR (m.sort_order=? AND i.id>?))` + filter + ` ORDER BY m.sort_order,i.id LIMIT ?`
		args = []any{collectionID, owner, order, order, bound.After, query, query, project, project, owner, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return page, err
	}
	type position struct {
		id    string
		order int64
	}
	positions := []position{}
	for rows.Next() {
		var value position
		if err = rows.Scan(&value.id, &value.order); err != nil {
			break
		}
		positions = append(positions, value)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return page, err
	}
	if len(positions) > limit {
		positions = positions[:limit]
		last := positions[len(positions)-1]
		bound.After = last.id
		if collectionID != "" {
			bound.Order = &last.order
		}
		raw, err := json.Marshal(bound)
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, value := range positions {
		item, err := readLiteratureItem(ctx, tx, owner, value.id)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	return page, tx.Commit()
}
