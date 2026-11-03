package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

const literatureStudyOperation = "literature.study-snapshot"

func studyRequestHash(id string, input LiteratureStudyGenerate) string {
	raw, _ := json.Marshal(struct {
		ItemID  string                  `json:"item_id"`
		Request LiteratureStudyGenerate `json:"request"`
	}{id, input})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// Replays are authorized against the current item/source, never just a receipt ID.
func (s *Store) ReplayLiteratureStudy(ctx context.Context, owner, id string, input LiteratureStudyGenerate) (LiteratureStudyView, bool, error) {
	var result LiteratureStudyView
	if !input.valid() || !validLiteratureID(owner) || !validLiteratureID(id) {
		return result, false, ErrLiteratureInvalid
	}
	tx, err := s.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, false, err
	}
	defer tx.Rollback()
	source, err := readLiteratureStudySource(ctx, tx, owner, id)
	if err != nil {
		return result, false, err
	}
	if source.Key != input.ExpectedSourceKey {
		return result, false, ErrLiteratureStudySourceChanged
	}
	found, err := lookupMutationResultTx(ctx, tx, owner, input.OperationID, literatureStudyOperation, studyRequestHash(id, input), &result)
	if err == nil && found && (result.SourceKey != source.Key || result.Snapshot == nil || !validStudySnapshot(*result.Snapshot)) {
		err = ErrLiteratureStudySourceChanged
	}
	if err == nil {
		err = tx.Commit()
	}
	return result, found, err
}

// Only publication is transactional. All source I/O and inference occur before
// this call; a short Store transaction owns the source/revision CAS and receipt.
func (s *Store) PublishLiteratureStudy(ctx context.Context, owner, id string, input LiteratureStudyGenerate, snapshot LiteratureStudySnapshot) (LiteratureStudyView, error) {
	var result LiteratureStudyView
	if !input.valid() || !validLiteratureID(owner) || !validLiteratureID(id) || !validStudySnapshot(snapshot) || snapshot.SourceKey != input.ExpectedSourceKey {
		return result, ErrLiteratureInvalid
	}
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, current, err := readLiteratureStudy(ctx, tx, owner, id, input.Language)
		if err != nil {
			return err
		}
		if source.Key != input.ExpectedSourceKey {
			return ErrLiteratureStudySourceChanged
		}
		found, err := lookupMutationResultTx(ctx, tx, owner, input.OperationID, literatureStudyOperation, studyRequestHash(id, input), &result)
		if err != nil || found {
			return err
		}
		if current.Revision != input.ExpectedRevision {
			return ErrLiteratureStudyPublicationConflict
		}
		if snapshot.Coverage == "abstract" {
			if source.Abstract == "" {
				return ErrLiteratureStudySourceChanged
			}
		} else if source.Version == nil || !source.Version.Available || source.Version.ID != snapshot.AttachmentVersionID {
			return ErrLiteratureStudySourceChanged
		}
		current.Revision++
		current.Snapshot = &snapshot
		raw, err := json.Marshal(snapshot)
		if err != nil || len(raw) > 40_000 {
			return ErrLiteratureInvalid
		}
		write, err := tx.ExecContext(ctx, `INSERT INTO literature_study_snapshots(owner_user_id,item_id,language,revision,source_key,snapshot_json) VALUES(?,?,?,?,?,?)
   ON CONFLICT(owner_user_id,item_id,language) DO UPDATE SET revision=excluded.revision,source_key=excluded.source_key,snapshot_json=excluded.snapshot_json WHERE literature_study_snapshots.revision=?`, owner, id, input.Language, current.Revision, source.Key, string(raw), input.ExpectedRevision)
		if err != nil {
			return err
		}
		count, err := write.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrLiteratureStudyPublicationConflict
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result = current
		return insertMutationResultTx(ctx, tx, owner, input.OperationID, literatureStudyOperation, studyRequestHash(id, input), result, s.now().UTC())
	})
	return result, err
}
