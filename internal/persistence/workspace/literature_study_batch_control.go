package workspace

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
)

func (s *Store) ControlLiteratureStudyBatch(ctx context.Context, owner string, c LiteratureStudyBatchCommand) (*LiteratureStudyBatch, error) {
	if !validLiteratureID(owner) || !c.valid() || c.Action == "advance" {
		return nil, ErrLiteratureInvalid
	}
	hash, err := mutationRequestHash(c)
	if err != nil {
		return nil, err
	}
	var result *LiteratureStudyBatch
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var ack literatureStudyBatchAck
		found, err := lookupMutationResultTx(ctx, tx, owner, c.OperationID, "literature.study-batch."+c.Action, hash, &ack)
		if err != nil {
			return err
		}
		r, err := readStudyBatch(ctx, tx, owner)
		if err != nil {
			return err
		}
		if found {
			if r == nil || r.batch.ID != ack.BatchID {
				return ErrLiteratureStudyBatchConflict
			}
			result = &r.batch
			return nil
		}
		if c.Action == "start" {
			if r != nil {
				for _, row := range r.batch.Rows {
					if row.State == "pending" || row.State == "running" {
						return ErrLiteratureStudyBatchConflict
					}
				}
				if r.token != "" {
					if r.expires > s.now().UnixMilli() {
						return ErrLiteratureStudyBatchBusy
					}
					// All rows are already terminal. Only this expired request's
					// bookkeeping is replaced; committed child receipts stay intact.
					r.token = ""
					r.expires = 0
				}
			} else {
				r = &studyBatchRecord{}
			}
			r.batch = LiteratureStudyBatch{ID: c.OperationID, Rows: make([]LiteratureStudyBatchRow, 0, len(c.ItemIDs))}
			for _, id := range c.ItemIDs {
				source, view, err := readLiteratureStudy(ctx, tx, owner, id, c.Language)
				if err != nil {
					return err
				}
				row := LiteratureStudyBatchRow{ItemID: id, Request: LiteratureStudyGenerate{OperationID: uuid.NewString(), Language: c.Language, ExpectedSourceKey: source.Key, ExpectedRevision: view.Revision}, State: "pending"}
				if view.Snapshot != nil {
					row.State = "succeeded"
					row.ResultRevision = view.Revision
					row.Reused = true
				}
				r.batch.Rows = append(r.batch.Rows, row)
			}
		} else {
			if r == nil || r.batch.ID != c.BatchID || r.batch.Revision != c.ExpectedRevision {
				return ErrLiteratureStudyBatchConflict
			}
			if c.Action == "pause" {
				r.batch.Paused = true
			} else {
				r.batch.Paused = false
				if c.Action == "retry" {
					for i := range r.batch.Rows {
						if r.batch.Rows[i].State == "failed" {
							r.batch.Rows[i].State = "pending"
							r.batch.Rows[i].Issue = ""
						}
					}
				}
			}
		}
		if err := saveStudyBatch(ctx, tx, owner, r); err != nil {
			return err
		}
		ack = literatureStudyBatchAck{r.batch.ID, r.batch.Revision}
		if err := insertMutationResultTx(ctx, tx, owner, c.OperationID, "literature.study-batch."+c.Action, hash, ack, s.now().UTC()); err != nil {
			return err
		}
		result = &r.batch
		return nil
	})
	return result, err
}
