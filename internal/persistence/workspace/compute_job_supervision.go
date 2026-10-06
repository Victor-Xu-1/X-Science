package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// SetComputeJobControlUnavailable preserves the last authoritative execution
// state. A failed control-plane request cannot prove that a workload exited.
func (s *Store) SetComputeJobControlUnavailable(ctx context.Context, userID, jobID, kind string) error {
	if kind != "control_unreachable" && kind != "control_configuration_required" {
		return errors.New("invalid compute control observation")
	}
	return s.updateComputeJobControlObservation(ctx, userID, jobID, kind)
}

func (s *Store) ClearComputeJobControlUnavailable(ctx context.Context, userID, jobID string) error {
	return s.updateComputeJobControlObservation(ctx, userID, jobID, "")
}

// The marker and its workbench event are committed together. Ownership and
// terminal state are checked inside the same transaction; a late probe cannot
// revive a cancelled job or erase an unrelated authoritative failure.
func (s *Store) updateComputeJobControlObservation(ctx context.Context, userID, jobID, kind string) error {
	if s == nil || s.db == nil {
		return ErrWorkspaceStoreClosed
	}
	userID, jobID = strings.TrimSpace(userID), strings.TrimSpace(jobID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := scanComputeJob(tx.QueryRowContext(ctx, `SELECT `+computeJobColumns+` FROM compute_workbench_jobs WHERE owner_user_id=? AND job_id=?`, userID, jobID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrComputeJobNotFound
		}
		return err
	}
	if job.State != ComputeJobPending && job.State != ComputeJobStaging && job.State != ComputeJobQueued && job.State != ComputeJobRunning && job.State != ComputeJobHarvesting {
		return ErrComputeJobTransition
	}
	previous := ""
	if job.ErrorKind != nil {
		previous = *job.ErrorKind
	}
	if previous != "" && previous != "control_unreachable" && previous != "control_configuration_required" {
		return ErrComputeJobTransition
	}
	if previous == kind {
		return nil
	}
	var marker, hint any
	job.ErrorKind, job.SystemHint = nil, nil
	if kind != "" {
		message := "Remote control is unavailable; execution outcome is unknown. Original job identity and outputs are retained for reconciliation."
		marker, hint = kind, message
		job.ErrorKind, job.SystemHint = &kind, &message
	}
	if _, err := tx.ExecContext(ctx, `UPDATE compute_workbench_jobs SET error_kind=?,system_hint=? WHERE owner_user_id=? AND job_id=?`, marker, hint, userID, jobID); err != nil {
		return err
	}
	if err := s.enqueueComputeJobUpdateTx(ctx, tx, userID, job); err != nil {
		return err
	}
	return tx.Commit()
}
