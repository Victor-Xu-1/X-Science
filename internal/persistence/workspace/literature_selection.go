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

type LiteratureSelectionReadOrder string

const LiteratureSelectionTitleAscending LiteratureSelectionReadOrder = "title-asc"

type LiteratureSelectionReadOptions struct{ Order LiteratureSelectionReadOrder }

type LiteratureRestoreTarget struct {
	ID               string `json:"id"`
	MetadataRevision int64  `json:"metadata_revision"`
}

type LiteratureRestorePreview struct {
	Schema     string                    `json:"schema"`
	Items      []LiteratureRestoreTarget `json:"items"`
	TotalCount int64                     `json:"total_count"`
}

func (s *Store) ListLiteratureItemIDsInLifecycle(ctx context.Context, owner string, filters LiteratureQueryFilters) (LiteratureSelection, error) {
	return s.ListLiteratureItemIDsWithReadOptions(ctx, owner, filters, LiteratureSelectionReadOptions{})
}

// Read options never enter catalogue mutation payloads or their durable identities.
func (s *Store) ListLiteratureItemIDsWithReadOptions(ctx context.Context, owner string, filters LiteratureQueryFilters, options LiteratureSelectionReadOptions) (LiteratureSelection, error) {
	empty := LiteratureSelection{Schema: "x-science.literature-selection.v1", ItemIDs: []string{}}
	items, err := s.literatureSelectionSnapshot(ctx, owner, filters, options, false)
	if err != nil {
		return empty, err
	}
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return LiteratureSelection{Schema: empty.Schema, ItemIDs: ids, TotalCount: int64(len(ids))}, nil
}

// Preview uses the same authorized read snapshot without hydrating deleted details.
func (s *Store) PreviewLiteratureRestore(ctx context.Context, owner string, filters LiteratureQueryFilters, options LiteratureSelectionReadOptions) (LiteratureRestorePreview, error) {
	empty := LiteratureRestorePreview{Schema: "x-science.literature-restore-preview.v1", Items: []LiteratureRestoreTarget{}}
	if filters.Lifecycle != LiteratureLifecycleDeleted {
		return empty, ErrLiteratureInvalid
	}
	items, err := s.literatureSelectionSnapshot(ctx, owner, filters, options, true)
	if err != nil {
		return empty, err
	}
	return LiteratureRestorePreview{Schema: empty.Schema, Items: items, TotalCount: int64(len(items))}, nil
}

func (s *Store) literatureSelectionSnapshot(ctx context.Context, owner string, filters LiteratureQueryFilters, options LiteratureSelectionReadOptions, revisions bool) ([]LiteratureRestoreTarget, error) {
	var empty []LiteratureRestoreTarget
	if options.Order != "" && options.Order != LiteratureSelectionTitleAscending {
		return empty, ErrLiteratureInvalid
	}
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
	order := scope.orderBy()
	if options.Order == LiteratureSelectionTitleAscending {
		order = "i.title ASC,i.id ASC"
	}
	columns := "i.id"
	if revisions {
		columns += ",i.metadata_revision"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+statement+` ORDER BY `+order, args...)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	items := []LiteratureRestoreTarget{}
	for rows.Next() {
		var item LiteratureRestoreTarget
		fields := []any{&item.ID}
		if revisions {
			fields = append(fields, &item.MetadataRevision)
		}
		if err := rows.Scan(fields...); err != nil {
			return empty, err
		}
		if revisions && item.MetadataRevision < 1 {
			return empty, ErrLiteratureInvalid
		}
		items = append(items, item)
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
	return items, nil
}
