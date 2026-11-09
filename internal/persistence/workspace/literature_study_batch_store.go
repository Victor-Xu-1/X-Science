package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type studyBatchRecord struct {
	batch         LiteratureStudyBatch
	token         string
	expires       int64
	priorID       string
	priorRevision int64
}

func readStudyBatch(ctx context.Context, query workspaceTransaction, owner string) (*studyBatchRecord, error) {
	var raw string
	r := &studyBatchRecord{}
	err := query.QueryRowContext(ctx, `SELECT batch_id,revision,checkpoint_json,request_token,request_expires_at FROM literature_study_batches WHERE owner_user_id=?`, owner).
		Scan(&r.priorID, &r.priorRevision, &raw, &r.token, &r.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > literatureStudyBatchBytes || json.Unmarshal([]byte(raw), &r.batch) != nil || r.batch.ID != r.priorID || r.batch.Revision != r.priorRevision || len(r.batch.Rows) < 1 || len(r.batch.Rows) > LiteratureStudyBatchLimit {
		return nil, ErrLiteratureInvalid
	}
	for _, row := range r.batch.Rows {
		if !validLiteratureID(row.ItemID) || !row.Request.valid() || (row.State != "pending" && row.State != "running" && row.State != "succeeded" && row.State != "failed") {
			return nil, ErrLiteratureInvalid
		}
	}
	r.batch.RequestExpiresAt = r.expires
	return r, nil
}

// The checkpoint is mutable, bounded state. Immutable command receipts remain
// in the original ledger and only acknowledge a committed transition.
func saveStudyBatch(ctx context.Context, tx *sql.Tx, owner string, r *studyBatchRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.batch.Revision >= literatureStudyMaxRevision {
		return ErrLiteratureStudyBatchConflict
	}
	r.batch.Revision++
	stored := r.batch
	stored.RequestExpiresAt = 0
	raw, err := json.Marshal(stored)
	if err != nil || len(raw) > literatureStudyBatchBytes {
		return ErrLiteratureInvalid
	}
	var result sql.Result
	if r.priorID == "" {
		result, err = tx.ExecContext(ctx, `INSERT INTO literature_study_batches(owner_user_id,batch_id,revision,checkpoint_json,request_token,request_expires_at) VALUES(?,?,?,?,?,?)`, owner, r.batch.ID, r.batch.Revision, string(raw), r.token, r.expires)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE literature_study_batches SET batch_id=?,revision=?,checkpoint_json=?,request_token=?,request_expires_at=? WHERE owner_user_id=? AND batch_id=? AND revision=?`, r.batch.ID, r.batch.Revision, string(raw), r.token, r.expires, owner, r.priorID, r.priorRevision)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLiteratureStudyBatchConflict
	}
	r.priorID, r.priorRevision = r.batch.ID, r.batch.Revision
	r.batch.RequestExpiresAt = r.expires
	return nil
}

func (s *Store) ReadLiteratureStudyBatch(ctx context.Context, owner string) (*LiteratureStudyBatch, error) {
	if !validLiteratureID(owner) {
		return nil, ErrLiteratureInvalid
	}
	r, err := readStudyBatch(ctx, s.readDatabase(), owner)
	if err != nil || r == nil {
		return nil, err
	}
	return &r.batch, nil
}
