package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// MemoryExtractionCoverageReceipt is an original immutable result, not a new
// cursor. The runtime must re-project Source and prove its examined prefix.
type MemoryExtractionCoverageReceipt struct {
	Source CompletedExtractionSource
	Result ExtractionCommitResult
}

// The inner query reads only a bounded rowid tail. Scope and authority filters
// run in SQLite before any payload leaves the DB; legacy needs no unindexed
// whole-frame sort or new migration. A missing chain means unknown coverage.
const memoryExtractionCoverageQuery = `WITH recent(row_id) AS MATERIALIZED (
	SELECT rowid FROM memory_extraction_receipts ORDER BY rowid DESC LIMIT 128
) SELECT receipt.source_key,receipt.source_json,receipt.source_sha256 FROM recent
	JOIN memory_extraction_receipts receipt ON receipt.rowid=recent.row_id
	WHERE receipt.owner_user_id=? AND receipt.project_id=? AND receipt.frame_id=?
	 AND receipt.frame_incarnation_id=?`

func (s *Store) ReadMemoryExtractionCoverageReceipts(ctx context.Context, source CompletedExtractionSource) ([]MemoryExtractionCoverageReceipt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("workspace store is closed")
	}
	identity, err := identifyExtractionSource(source)
	if err != nil {
		return nil, err
	}
	database := s.readDB
	if database == nil {
		database = s.db
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireExtractionFrameTx(ctx, tx, identity); err != nil {
		return nil, err
	}
	query := memoryExtractionCoverageQuery
	args := []any{identity.source.OwnerUserID, identity.source.ProjectID, identity.source.FrameID, identity.incarnation}
	if proof := identity.source.Transcript; proof != nil {
		query += ` AND json_extract(receipt.source_json,'$.Transcript.StreamUID')=?
		 AND json_extract(receipt.source_json,'$.Transcript.StreamEpoch')=?
		 AND json_extract(receipt.source_json,'$.Transcript.PublicationSeq')<?
		 AND EXISTS (SELECT 1 FROM transcript_branch_events member
		 WHERE member.stream_uid=? AND member.branch_id=?
		 AND member.event_id=json_extract(receipt.source_json,'$.Transcript.EventID'))`
		args = append(args, proof.StreamUID, proof.StreamEpoch, proof.PublicationSeq, proof.StreamUID, proof.BranchID)
	} else {
		query += ` AND json_extract(receipt.source_json,'$.Legacy.SessionID')=?
		 AND json_extract(receipt.source_json,'$.Legacy.FinishEventID')<?`
		args = append(args, identity.source.Legacy.SessionID, identity.source.Legacy.FinishEventID)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read memory extraction coverage: %w", err)
	}
	var encoded []extractionStoredSource
	for rows.Next() {
		var value extractionStoredSource
		if err := rows.Scan(&value.key, &value.raw, &value.digest); err != nil {
			_ = rows.Close()
			return nil, err
		}
		encoded = append(encoded, value)
	}
	rowsErr, closeErr := rows.Err(), rows.Close()
	if rowsErr != nil {
		return nil, rowsErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	result := make([]MemoryExtractionCoverageReceipt, 0, len(encoded))
	for _, value := range encoded {
		prior, err := identifyStoredExtractionSource(identity, value)
		if err != nil {
			return nil, err
		}
		if proof := identity.source.Transcript; proof != nil {
			if prior.source.Transcript.PublicationSeq >= proof.PublicationSeq {
				return nil, ErrExtractionPlanConflict
			}
		} else if prior.source.Legacy.FinishEventID >= identity.source.Legacy.FinishEventID {
			return nil, ErrExtractionPlanConflict
		}
		saved, found, err := readExtractionReceiptTx(ctx, tx, prior, extractionReceiptStrict)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrExtractionPlanConflict
		}
		result = append(result, MemoryExtractionCoverageReceipt{Source: prior.source, Result: saved.result})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(left, right int) bool {
		if identity.source.Transcript != nil {
			return result[left].Source.Transcript.PublicationSeq < result[right].Source.Transcript.PublicationSeq
		}
		return result[left].Source.Legacy.FinishEventID < result[right].Source.Legacy.FinishEventID
	})
	return result, nil
}
