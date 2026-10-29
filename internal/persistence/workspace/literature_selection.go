package workspace

import (
	"context"
	"database/sql"
	"errors"
)

type LiteratureSelection struct {
	Schema     string   `json:"schema"`
	ItemIDs    []string `json:"item_ids"`
	TotalCount int64    `json:"total_count"`
}

// ListLiteratureItemIDs resolves all matches in one read snapshot. It does not
// hydrate metadata or attachments, and never returns a truncated success.
func (s *Store) ListLiteratureItemIDs(ctx context.Context, owner, query, project, collection string, readingStatus LiteratureReadingStatus) (LiteratureSelection, error) {
	return s.ListLiteratureItemIDsInLifecycle(ctx, owner, LiteratureQueryFilters{Query: query, ProjectID: project, CollectionID: collection, ReadingStatus: readingStatus})
}

func (s *Store) ListLiteratureItemIDsInLifecycle(ctx context.Context, owner string, filters LiteratureQueryFilters) (LiteratureSelection, error) {
	empty := LiteratureSelection{Schema: "x-science.literature-selection.v1", ItemIDs: []string{}}
	scope := literatureScope(owner, filters)
	if err := scope.validate(); err != nil {
		return empty, err
	}
	database := s.readDatabase()
	if database == nil {
		return empty, errors.New("workspace store is closed")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err := scope.authorize(ctx, tx); err != nil {
		return empty, err
	}
	statement, args := scope.sql()
	rows, err := tx.QueryContext(ctx, `SELECT i.id`+statement+` ORDER BY `+scope.orderBy(), args...)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return empty, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return empty, err
	}
	if err := rows.Close(); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return LiteratureSelection{Schema: empty.Schema, ItemIDs: ids, TotalCount: int64(len(ids))}, nil
}
