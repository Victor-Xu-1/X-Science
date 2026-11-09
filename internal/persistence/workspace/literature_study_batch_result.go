package workspace

import (
	"context"
	"database/sql"
)

func (s *Store) PublishLiteratureStudyBatchRow(ctx context.Context, owner string, claim LiteratureStudyBatchClaim, index int, snapshot LiteratureStudySnapshot) (LiteratureStudyView, error) {
	var result LiteratureStudyView
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		r, err := s.studyBatchClaimTx(ctx, tx, owner, claim, index, false)
		if err != nil {
			return err
		}
		row := &r.batch.Rows[index]
		result, err = s.publishLiteratureStudyTx(ctx, tx, owner, row.ItemID, row.Request, snapshot)
		if err != nil {
			return err
		}
		row.State = "succeeded"
		row.Issue = ""
		row.ResultRevision = result.Revision
		return saveStudyBatch(ctx, tx, owner, r)
	})
	return result, err
}

// A request lost after a publication is reconciled through its exact immutable
// child receipt. Saved pre-existing results keep an explicit revision reference.
func (s *Store) ReconcileLiteratureStudyBatchRow(ctx context.Context, owner string, claim LiteratureStudyBatchClaim, index int) (bool, error) {
	found := false
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		r, err := s.studyBatchClaimTx(ctx, tx, owner, claim, index, false)
		if err != nil {
			return err
		}
		row := &r.batch.Rows[index]
		source, view, err := readLiteratureStudy(ctx, tx, owner, row.ItemID, row.Request.Language)
		if err != nil {
			return err
		}
		if source.Key != row.Request.ExpectedSourceKey {
			return ErrLiteratureStudySourceChanged
		}
		var replay LiteratureStudyView
		found, err = lookupMutationResultTx(ctx, tx, owner, row.Request.OperationID, literatureStudyOperation, studyRequestHash(row.ItemID, row.Request), &replay)
		if err != nil {
			return err
		}
		if found {
			if replay.SourceKey != source.Key || replay.Snapshot == nil || !validStudySnapshot(*replay.Snapshot) || replay.Revision != row.Request.ExpectedRevision+1 {
				return ErrLiteratureInvalid
			}
			view = replay
		} else if view.Snapshot != nil {
			if view.Revision != row.Request.ExpectedRevision {
				return ErrLiteratureStudyPublicationConflict
			}
			found = true
			row.Reused = true
		}
		if !found {
			return nil
		}
		row.State = "succeeded"
		row.Issue = ""
		row.ResultRevision = view.Revision
		return saveStudyBatch(ctx, tx, owner, r)
	})
	return found, err
}

func (s *Store) ReadLiteratureStudyBatchResult(ctx context.Context, owner, batchID string, index int) (LiteratureStudyView, error) {
	var result LiteratureStudyView
	if !validLiteratureID(owner) || !studyBatchUUID(batchID) || index < 0 {
		return result, ErrLiteratureInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	r, err := readStudyBatch(ctx, tx, owner)
	if err != nil {
		return result, err
	}
	if r == nil || r.batch.ID != batchID || index >= len(r.batch.Rows) {
		return result, ErrLiteratureStudyBatchConflict
	}
	row := r.batch.Rows[index]
	if row.State != "succeeded" {
		return result, ErrLiteratureStudyBatchConflict
	}
	source, current, err := readLiteratureStudy(ctx, tx, owner, row.ItemID, row.Request.Language)
	if err != nil {
		return result, err
	}
	if source.Key != row.Request.ExpectedSourceKey {
		return result, ErrLiteratureStudySourceChanged
	}
	if row.Reused {
		result = current
	} else {
		found, err := lookupMutationResultTx(ctx, tx, owner, row.Request.OperationID, literatureStudyOperation, studyRequestHash(row.ItemID, row.Request), &result)
		if err != nil {
			return result, err
		}
		if !found {
			return result, ErrLiteratureStudyBatchConflict
		}
	}
	if result.SourceKey != source.Key || result.Revision != row.ResultRevision || result.Snapshot == nil || !validStudySnapshot(*result.Snapshot) {
		return LiteratureStudyView{}, ErrLiteratureStudyPublicationConflict
	}
	return result, tx.Commit()
}
