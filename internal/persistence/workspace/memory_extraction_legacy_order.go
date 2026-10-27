package workspace

import (
	"context"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Unlike optional coverage's recent tail, absence here must be proved across
// this complete legacy scope. The original frame receipt index supplies its
// owner/incarnation prefix and null Transcript keys. Pages bound retained
// payloads to 128; total work scales with this scope's receipts, not a fixed
// global tail. No new index, second ledger or source-order authority is added.
const legacyExtractionOrderQuery = `SELECT rowid,source_key,source_json,source_sha256
	FROM memory_extraction_receipts
	WHERE frame_id=? AND owner_user_id=? AND frame_incarnation_id=? AND project_id=?
	 AND json_extract(source_json,'$.Transcript.StreamUID') IS NULL
	 AND json_extract(source_json,'$.Transcript.StreamEpoch') IS NULL
	 AND json_extract(source_json,'$.Transcript.PublicationSeq') IS NULL
	 AND json_extract(source_json,'$.Legacy.SessionID')=?`

func fenceLegacyExtractionSourceOrderTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, current extractionSourceIdentity, check LegacyExtractionOrderCheck) error {
	if check == nil {
		return ErrExtractionPlanConflict
	}
	var after *int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		query := legacyExtractionOrderQuery
		args := []any{current.source.FrameID, current.source.OwnerUserID, current.incarnation,
			current.source.ProjectID, current.source.Legacy.SessionID}
		if after != nil {
			query += " AND rowid>?"
			args = append(args, *after)
		}
		rows, err := tx.QueryContext(ctx, query+" ORDER BY rowid ASC LIMIT 128", args...)
		if err != nil {
			return fmt.Errorf("read legacy completed memory extraction order: %w", err)
		}
		type item struct {
			rowid  int64
			source extractionStoredSource
		}
		var page []item
		for rows.Next() {
			var value item
			if err := rows.Scan(&value.rowid, &value.source.key, &value.source.raw, &value.source.digest); err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, value)
		}
		rowsErr, closeErr := rows.Err(), rows.Close()
		if rowsErr != nil {
			return rowsErr
		}
		if closeErr != nil {
			return closeErr
		}
		for _, value := range page {
			prior, err := identifyStoredExtractionSource(current, value.source)
			if err != nil {
				return err
			}
			if prior.source.Legacy.FinishEventID <= current.source.Legacy.FinishEventID {
				continue
			}
			if _, found, err := readExtractionReceiptTx(ctx, tx, prior, extractionReceiptStrict); err != nil {
				return err
			} else if !found {
				return ErrExtractionPlanConflict
			}
			selected, err := check(prior.source)
			if err != nil {
				return err
			}
			if selected {
				return fmt.Errorf("%w: newer selected legacy completion already advanced extraction", ErrExtractionSourceChanged)
			}
		}
		if len(page) < 128 {
			return nil
		}
		last := page[len(page)-1].rowid
		after = &last
	}
}
