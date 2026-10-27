package workspace

import "context"

// One filter authority serves ordinary pages, their count, and complete ID reads.
type literatureQueryScope struct {
	owner, query, project, collection string
	readingStatus                     LiteratureReadingStatus
}

func (scope literatureQueryScope) validate() error {
	if !validLiteratureID(scope.owner) || len(scope.query) > 512 ||
		scope.collection != "" && !validLiteratureID(scope.collection) ||
		scope.readingStatus != "" && !validLiteratureReadingStatus(scope.readingStatus) {
		return ErrLiteratureInvalid
	}
	return nil
}

func (scope literatureQueryScope) authorize(ctx context.Context, query literatureItemQuery) error {
	if scope.collection != "" {
		_, err := readLiteratureCollection(ctx, query, scope.owner, scope.collection)
		return err
	}
	return nil
}

func (scope literatureQueryScope) sql() (string, []any) {
	statement := ` FROM literature_items i`
	if scope.collection != "" {
		statement += ` JOIN literature_collection_items m ON m.item_id=i.id`
	}
	statement += ` WHERE i.owner_user_id=? AND i.deleted_at IS NULL
 AND (?='' OR instr(lower(i.title),lower(?))>0)
 AND (?='' OR EXISTS(SELECT 1 FROM project_literature p JOIN projects project ON project.id=p.project_id
 WHERE p.item_id=i.id AND p.project_id=? AND project.user_id=?))
 AND (?='' OR i.reading_status=?)`
	args := []any{scope.owner, scope.query, scope.query, scope.project, scope.project, scope.owner, scope.readingStatus, scope.readingStatus}
	if scope.collection != "" {
		statement += ` AND m.collection_id=?`
		args = append(args, scope.collection)
	}
	return statement, args
}

func (scope literatureQueryScope) orderBy() string {
	if scope.collection != "" {
		return `m.sort_order,i.id`
	}
	return `i.id`
}

func (scope literatureQueryScope) count(ctx context.Context, query literatureItemQuery) (int64, error) {
	statement, args := scope.sql()
	var count int64
	err := query.QueryRowContext(ctx, `SELECT COUNT(*)`+statement, args...).Scan(&count)
	return count, err
}
