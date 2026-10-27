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
	Collection                   string                  `json:",omitempty"`
	Order                        *int64                  `json:",omitempty"`
	ReadingStatus                LiteratureReadingStatus `json:",omitempty"`
}

func (s *Store) ListLiteratureItemsInCollection(ctx context.Context, owner, query, project, collectionID, cursor string, limit int) (LiteraturePage, error) {
	return s.ListLiteratureItemsWithReadingStatus(ctx, owner, query, project, collectionID, "", cursor, limit)
}

func (s *Store) ListLiteratureItemsWithReadingStatus(ctx context.Context, owner, query, project, collectionID string, readingStatus LiteratureReadingStatus, cursor string, limit int) (LiteraturePage, error) {
	page := LiteraturePage{Schema: "x-science.literature-page.v1", Items: []LiteratureItem{}}
	scope := literatureQueryScope{owner, query, project, collectionID, readingStatus}
	if scope.validate() != nil || limit < 1 || limit > 100 || len(cursor) > 4096 {
		return page, ErrLiteratureInvalid
	}
	bound := literatureCursor{Query: query, Project: project, Owner: owner, Collection: collectionID, ReadingStatus: readingStatus}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var decoded literatureCursor
		if err != nil || json.Unmarshal(raw, &decoded) != nil || decoded.Query != query || decoded.Project != project ||
			decoded.Owner != owner || decoded.Collection != collectionID || decoded.ReadingStatus != readingStatus ||
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
	if err := scope.authorize(ctx, tx); err != nil {
		return page, err
	}
	total, err := scope.count(ctx, tx)
	if err != nil {
		return page, err
	}
	from, args := scope.sql()
	statement := `SELECT i.id,0` + from
	if collectionID != "" {
		order := int64(-1)
		if bound.Order != nil {
			order = *bound.Order
		}
		statement = `SELECT i.id,m.sort_order` + from + ` AND (m.sort_order>? OR (m.sort_order=? AND i.id>?))`
		args = append(args, order, order, bound.After)
	} else {
		statement += ` AND i.id>?`
		args = append(args, bound.After)
	}
	statement += ` ORDER BY ` + scope.orderBy() + ` LIMIT ?`
	args = append(args, limit+1)
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
	if err := tx.Commit(); err != nil {
		return page, err
	}
	page.TotalCount = &total
	return page, nil
}
