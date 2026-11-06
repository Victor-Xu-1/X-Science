package workspace

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"time"
)

// No work survives its HTTP owner. This finite claim only fences duplicate
// requests and crash recovery; it is not an inference scheduler or heartbeat.
func (s *Store) ClaimLiteratureStudyBatch(ctx context.Context, owner string, c LiteratureStudyBatchCommand, budget time.Duration) (LiteratureStudyBatchClaim, *LiteratureStudyBatch, error) {
	var claim LiteratureStudyBatchClaim
	var result *LiteratureStudyBatch
	if !validLiteratureID(owner) || !c.valid() || c.Action != "advance" || budget <= 0 || budget > 5*time.Minute {
		return claim, nil, ErrLiteratureInvalid
	}
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		r, err := readStudyBatch(ctx, tx, owner)
		if err != nil {
			return err
		}
		if r == nil || r.batch.ID != c.BatchID {
			return ErrLiteratureStudyBatchConflict
		}
		if r.batch.Paused {
			result = &r.batch
			return nil
		}
		if r.token != "" && r.expires > s.now().UnixMilli() {
			return ErrLiteratureStudyBatchBusy
		}
		if r.token != "" {
			for i := range r.batch.Rows {
				if r.batch.Rows[i].State == "running" {
					r.batch.Rows[i].State = "pending"
				}
			}
			r.token = ""
			r.expires = 0
		}
		for _, index := range c.Rows {
			if index >= len(r.batch.Rows) {
				return ErrLiteratureInvalid
			}
			if r.batch.Rows[index].State != "pending" {
				continue
			}
			r.batch.Rows[index].State = "running"
			claim.Rows = append(claim.Rows, index)
		}
		if len(claim.Rows) > 0 {
			claim.BatchID = r.batch.ID
			claim.Token = uuid.NewString()
			r.token = claim.Token
			r.expires = s.now().Add(budget).UnixMilli()
		}
		if err := saveStudyBatch(ctx, tx, owner, r); err != nil {
			return err
		}
		result = &r.batch
		return nil
	})
	return claim, result, err
}

func (s *Store) studyBatchClaimTx(ctx context.Context, tx *sql.Tx, owner string, claim LiteratureStudyBatchClaim, index int, allowExpired bool) (*studyBatchRecord, error) {
	r, err := readStudyBatch(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	if r == nil || r.batch.ID != claim.BatchID || claim.Token == "" || r.token != claim.Token || !allowExpired && r.expires <= s.now().UnixMilli() {
		return nil, ErrLiteratureStudyBatchClaim
	}
	if index >= 0 {
		included := false
		for _, row := range claim.Rows {
			included = included || row == index
		}
		if !included || index >= len(r.batch.Rows) || r.batch.Rows[index].State != "running" {
			return nil, ErrLiteratureStudyBatchClaim
		}
	}
	return r, nil
}

func (s *Store) FinishLiteratureStudyBatch(ctx context.Context, owner string, claim LiteratureStudyBatchClaim, interrupted bool) error {
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		r, err := s.studyBatchClaimTx(ctx, tx, owner, claim, -1, true)
		if err != nil {
			return err
		}
		for _, i := range claim.Rows {
			if i >= 0 && i < len(r.batch.Rows) && r.batch.Rows[i].State == "running" {
				r.batch.Rows[i].State = "pending"
				interrupted = true
			}
		}
		if interrupted {
			r.batch.Paused = true
		}
		r.token = ""
		r.expires = 0
		return saveStudyBatch(ctx, tx, owner, r)
	})
}

func (s *Store) FailLiteratureStudyBatchRow(ctx context.Context, owner string, claim LiteratureStudyBatchClaim, index int, issue string, interrupted bool) error {
	if issue == "" || len(issue) > 128 {
		return ErrLiteratureInvalid
	}
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		r, err := s.studyBatchClaimTx(ctx, tx, owner, claim, index, false)
		if err != nil {
			return err
		}
		row := &r.batch.Rows[index]
		row.State = "failed"
		row.Issue = issue
		if interrupted {
			row.State = "pending"
			row.Issue = ""
		}
		r.batch.Paused = true
		return saveStudyBatch(ctx, tx, owner, r)
	})
}
