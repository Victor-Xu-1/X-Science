package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CountProjectPathDurableReferences reports how many persisted project rows
// reference path. excludeProjectID, when set, is omitted so callers can ignore
// the row they are about to commit.
func (s *Store) CountProjectPathDurableReferences(ctx context.Context, path, excludeProjectID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("workspace store is closed")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}
	query := `SELECT COUNT(*) FROM projects WHERE path = ?`
	args := []any{path}
	if exclude := strings.TrimSpace(excludeProjectID); exclude != "" {
		query += ` AND id <> ?`
		args = append(args, exclude)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count project path durable references: %w", err)
	}
	return count, nil
}
