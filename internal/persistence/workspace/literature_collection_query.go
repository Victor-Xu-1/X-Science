package workspace

import (
	"context"
	"database/sql"
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
	return s.ListLiteratureItemsInLifecycle(ctx, owner, LiteratureQueryFilters{Query: query, ProjectID: project, CollectionID: collectionID, ReadingStatus: readingStatus}, cursor, limit)
}

func (s *Store) ListLiteratureItemsInLifecycle(ctx context.Context, owner string, filters LiteratureQueryFilters, cursor string, limit int) (LiteraturePage, error) {
	return s.ListLiteratureItemsInLifecycleWithReadOptions(ctx, owner, filters, cursor, limit, LiteraturePageReadOptions{})
}

func (s *Store) ListLiteratureItemsInLifecycleWithReadOptions(ctx context.Context, owner string, filters LiteratureQueryFilters, cursor string, limit int, options LiteraturePageReadOptions) (LiteraturePage, error) {
	page := LiteraturePage{Schema: "x-science.literature-page.v1", Items: []LiteratureItem{}}
	scope := literatureScope(owner, filters)
	collectionID := filters.CollectionID
	if scope.validate() != nil || !options.Sort.valid() || limit < 1 || limit > 100 || len(cursor) > 4096 {
		return page, ErrLiteratureInvalid
	}
	yearExpression := options.Sort.yearExpression()
	if yearExpression != "" && literatureYearKeyRegistrationError != nil {
		return page, literatureYearKeyRegistrationError
	}
	dateExpression := options.Sort.dateExpression()
	if dateExpression != "" && literatureTimeKeyRegistrationError != nil {
		return page, literatureTimeKeyRegistrationError
	}
	bound := literatureCursor{Query: filters.Query, Project: filters.ProjectID, Owner: owner, Collection: collectionID, ReadingStatus: filters.ReadingStatus}
	var titleAnchor literatureTitleAnchor
	var dateAnchor literatureDateAnchor
	var yearAnchor literatureYearAnchor
	if cursor != "" {
		if options.Sort == "" {
			decoded, err := decodeLiteratureCursor(cursor, bound, filters.Lifecycle)
			if err != nil {
				return page, err
			}
			bound = decoded
		} else if yearExpression != "" {
			decoded, err := decodeLiteratureYearCursor(cursor, scope, options.Sort)
			if err != nil {
				return page, err
			}
			yearAnchor = decoded
		} else if dateExpression != "" {
			decoded, err := decodeLiteratureDateCursor(cursor, scope, options.Sort)
			if err != nil {
				return page, err
			}
			dateAnchor = decoded
		} else {
			decoded, err := decodeLiteratureTitleCursor(cursor, scope, options.Sort)
			if err != nil {
				return page, err
			}
			titleAnchor = decoded
		}
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
	statement := `SELECT i.id,0,i.title` + from
	orderBy := scope.orderBy()
	if yearExpression != "" {
		statement = `SELECT i.id,0,i.title,` + yearExpression + from
		orderBy = options.Sort.orderBy()
		if cursor != "" {
			key, title, err := readLiteratureYearAnchor(ctx, tx, scope, options.Sort, yearAnchor)
			if err != nil {
				return page, err
			}
			continuation, values := literatureYearContinuation(yearExpression, key, title, yearAnchor.id, options.Sort == LiteraturePageYearAscending)
			statement += continuation
			args = append(args, values...)
		}
	} else if dateExpression != "" {
		statement = `SELECT i.id,0,i.title,` + dateExpression + from
		orderBy = options.Sort.orderBy()
		if cursor != "" {
			key, err := readLiteratureDateAnchor(ctx, tx, scope, options.Sort, dateAnchor)
			if err != nil {
				return page, err
			}
			if key.Valid {
				comparison := "<"
				if options.Sort == LiteraturePageCreatedAscending {
					comparison = ">"
				}
				statement += ` AND (` + dateExpression + comparison + `? OR (` + dateExpression + `=? AND i.id>?) OR ` + dateExpression + ` IS NULL)`
				args = append(args, key.String, key.String, dateAnchor.id)
			} else {
				statement += ` AND (` + dateExpression + ` IS NULL AND i.id>?)`
				args = append(args, dateAnchor.id)
			}
		}
	} else if options.Sort != "" {
		orderBy = options.Sort.orderBy()
		if cursor != "" {
			title, err := readLiteratureTitleAnchor(ctx, tx, scope, titleAnchor)
			if err != nil {
				return page, err
			}
			comparison := ">"
			if options.Sort == LiteraturePageTitleDescending {
				comparison = "<"
			}
			statement += ` AND (i.title` + comparison + `? OR (i.title=? AND i.id>?))`
			args = append(args, title, title, titleAnchor.id)
		}
	} else if collectionID != "" {
		order := int64(-1)
		if bound.Order != nil {
			order = *bound.Order
		}
		statement = `SELECT i.id,m.sort_order,i.title` + from + ` AND (m.sort_order>? OR (m.sort_order=? AND i.id>?))`
		args = append(args, order, order, bound.After)
	} else {
		statement += ` AND i.id>?`
		args = append(args, bound.After)
	}
	statement += ` ORDER BY ` + orderBy + ` LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return page, err
	}
	type position struct {
		id      string
		order   int64
		title   string
		dateKey sql.NullString
		yearKey sql.NullInt64
	}
	positions := []position{}
	for rows.Next() {
		var value position
		if yearExpression != "" {
			err = rows.Scan(&value.id, &value.order, &value.title, &value.yearKey)
		} else if dateExpression != "" {
			err = rows.Scan(&value.id, &value.order, &value.title, &value.dateKey)
		} else {
			err = rows.Scan(&value.id, &value.order, &value.title)
		}
		if err != nil {
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
		if collectionID != "" && options.Sort == "" {
			bound.Order = &last.order
		}
		var encoded string
		if options.Sort == "" {
			encoded, err = encodeLiteratureCursor(bound, filters.Lifecycle)
		} else if yearExpression != "" {
			encoded, err = encodeLiteratureYearCursor(literatureYearAnchor{id: last.id, keyHash: literatureYearHash(last.yearKey), titleHash: literatureTitleHash(last.title), known: last.yearKey.Valid}, scope, options.Sort)
		} else if dateExpression != "" {
			encoded, err = encodeLiteratureDateCursor(literatureDateAnchor{id: last.id, keyHash: literatureDateHash(last.dateKey), known: last.dateKey.Valid}, scope, options.Sort)
		} else {
			encoded, err = encodeLiteratureTitleCursor(literatureTitleAnchor{id: last.id, titleHash: literatureTitleHash(last.title)}, scope, options.Sort)
		}
		if err != nil {
			return page, err
		}
		page.NextCursor = encoded
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
