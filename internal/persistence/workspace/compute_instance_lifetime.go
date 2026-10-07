package workspace

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The earliest owned receipt is conservative across warm-job reuse. A later
// job timestamp cannot extend an already-created physical instance.
func (s *Store) ComputeInstanceDeadline(ctx context.Context, owner, provider, externalID string) (time.Time, bool, error) {
	if s == nil || s.db == nil || ctx == nil || owner == "" || provider == "" || externalID == "" {
		return time.Time{}, false, errors.New("compute instance identity required")
	}
	var epoch sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MIN(CASE WHEN json_valid(hardware_json) AND json_type(hardware_json,'$.sandbox_deadline_epoch')='integer' AND json_extract(hardware_json,'$.sandbox_deadline_epoch')>0 THEN json_extract(hardware_json,'$.sandbox_deadline_epoch') END) FROM compute_workbench_jobs WHERE owner_user_id=? AND provider=? AND external_id=?`, owner, provider, externalID).Scan(&epoch)
	if err != nil || !epoch.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(epoch.Int64, 0).UTC(), true, nil
}
