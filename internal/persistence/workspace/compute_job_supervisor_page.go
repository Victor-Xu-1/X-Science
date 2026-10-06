package workspace

import (
	"context"
	"strings"
)

type OwnedComputeJobPage struct {
	Jobs       []OwnedComputeJob
	NextCursor string
}

// This cursor bounds one query, not the active inventory. Supervisors drain all
// pages, including queued jobs, without dropping another provider family.
func (s *Store) ListSupervisedComputeJobsPage(ctx context.Context, cursor string, limit int) (OwnedComputeJobPage, error) {
	if s == nil || s.db == nil {
		return OwnedComputeJobPage{}, ErrWorkspaceStoreClosed
	}
	if limit <= 0 || limit > ComputeJobPageMax {
		limit = ComputeJobPageMax
	}
	query := `SELECT owner_user_id,` + computeJobColumns + ` FROM compute_workbench_jobs
		WHERE provider_family IN ('ssh','byoc') AND state IN ('pending','staging','queued','running','harvesting')`
	args := []any{}
	if strings.TrimSpace(cursor) != "" {
		at, id, err := decodeComputeJobCursor(cursor)
		if err != nil {
			return OwnedComputeJobPage{}, err
		}
		query += ` AND (started_at<? OR (started_at=? AND job_id>?))`
		args = append(args, at, at, id)
	}
	query += ` ORDER BY started_at DESC,job_id ASC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return OwnedComputeJobPage{}, err
	}
	defer rows.Close()
	page := OwnedComputeJobPage{Jobs: make([]OwnedComputeJob, 0, limit+1)}
	for rows.Next() {
		var owned OwnedComputeJob
		owned.Job, err = scanComputeJob(ownedComputeJobScanner{scanner: rows, owner: &owned.OwnerUserID})
		if err != nil {
			return OwnedComputeJobPage{}, err
		}
		page.Jobs = append(page.Jobs, owned)
	}
	if err := rows.Err(); err != nil {
		return OwnedComputeJobPage{}, err
	}
	if len(page.Jobs) > limit {
		page.Jobs = page.Jobs[:limit]
		last := page.Jobs[limit-1].Job
		page.NextCursor = encodeComputeJobCursor(last.StartedAt, last.JobID)
	}
	return page, nil
}

type ownedComputeJobScanner struct {
	scanner interface{ Scan(...any) error }
	owner   *string
}

func (s ownedComputeJobScanner) Scan(dest ...any) error {
	return s.scanner.Scan(append([]any{s.owner}, dest...)...)
}
