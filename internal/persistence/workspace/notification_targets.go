package workspace

import (
	"context"
	"errors"
	"strings"
)

// NotificationTargetIncarnations is an owner-scoped, bounded read. Missing or
// foreign IDs are absent, not rebound to a new notification target.
func (s *Store) NotificationTargetIncarnations(ctx context.Context, owner string, ids []string) (map[string]string, error) {
	if s.readDatabase() == nil || owner == "" || len(ids) > 1200 {
		return nil, errors.New("invalid notification target read")
	}
	result := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += 128 {
		end := min(start+128, len(ids))
		args := []any{owner}
		marks := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
			marks = append(marks, "?")
		}
		rows, err := s.readDatabase().QueryContext(ctx, `SELECT frame.id,frame.incarnation_id FROM frames frame
			JOIN projects project ON project.id=frame.project_id WHERE project.user_id=? AND frame.id IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, incarnation string
			if err := rows.Scan(&id, &incarnation); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = incarnation
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
