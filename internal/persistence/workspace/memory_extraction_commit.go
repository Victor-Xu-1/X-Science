package workspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

// CommitMemoryExtraction commits only prepared values. All model, classifier
// and repair calls finish before this short BEGIN IMMEDIATE write boundary.
// An uncertain COMMIT returns an error; a later attempt resolves its immutable
// receipt instead of repeating the accepted mutations.
func (s *Store) CommitMemoryExtraction(ctx context.Context, input ExtractionCommitInput) (ExtractionCommitResult, error) {
	if s == nil || s.db == nil {
		return ExtractionCommitResult{}, errors.New("workspace store is closed")
	}
	identity, err := identifyExtractionSource(input.Source)
	if err != nil {
		return ExtractionCommitResult{}, err
	}
	input.Source = identity.source
	input.InputSHA256 = normalizedExtractionDigest(input.InputSHA256)
	if input.InputSHA256 == "" || input.CursorBefore < 0 || input.CursorNext < 0 || input.DeltaCount < 0 ||
		input.Gate == "" || input.Gate != strings.TrimSpace(input.Gate) || len(input.Gate) > 64 ||
		input.AdvanceCursor && (!input.Overshoot && input.CursorNext < input.CursorBefore || input.Overshoot && input.CursorNext >= input.CursorBefore) {
		return ExtractionCommitResult{}, errors.New("memory extraction commit input is invalid")
	}
	input.Plan, err = normalizeExtractionPlan(input.Source, input.Plan)
	if err != nil {
		return ExtractionCommitResult{}, err
	}
	planDigest, err := extractionPlanDigest(input)
	if err != nil {
		return ExtractionCommitResult{}, err
	}
	repository, err := s.TranscriptRepository(ctx)
	if err != nil {
		return ExtractionCommitResult{}, err
	}
	var result ExtractionCommitResult
	_, err = repository.RunImmediateWithOutcome(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := requireExtractionFrameTx(ctx, tx, identity); err != nil {
			return err
		}
		saved, found, err := readExtractionReceiptTx(ctx, tx, identity, extractionReceiptStrict)
		if err != nil {
			return err
		}
		if found {
			if saved.inputDigest != input.InputSHA256 || saved.planDigest != planDigest {
				return ErrExtractionPlanConflict
			}
			result = saved.result
			result.Replayed = true
			if input.DeliveryClaim != nil {
				return settleMemoryExtractionClaimTx(ctx, tx, *input.DeliveryClaim, identity.key, "")
			}
			return nil
		}
		if input.DeliveryClaim != nil {
			if err := requireMemoryExtractionClaimTx(ctx, tx, *input.DeliveryClaim, identity.key); err != nil {
				return err
			}
			var cancelled bool
			if err := tx.QueryRowContext(ctx, `SELECT status IN ('cancelled','canceled') FROM frames WHERE id=?`, input.Source.FrameID).Scan(&cancelled); err != nil {
				return err
			}
			if cancelled {
				return ErrExtractionSourceChanged
			}
		}
		if err := requireFreshExtractionSourceTx(ctx, tx, identity); err != nil {
			return err
		}
		cursor, err := extractionCursorTx(ctx, tx, input.Source.FrameID)
		if err != nil {
			return err
		}
		if cursor != input.CursorBefore {
			return ErrExtractionCursorChanged
		}
		if input.AdvanceCursor || input.Overshoot {
			if err := fenceExtractionSourceOrderTx(ctx, tx, identity, input.LegacyOrderCheck); err != nil {
				return err
			}
		}
		applied, err := s.applyExtractionPlanTx(ctx, tx, input.Source, input.Plan)
		if err != nil {
			return err
		}
		result = ExtractionCommitResult{
			InputSHA256: input.InputSHA256, Gate: input.Gate, CursorBefore: cursor, CursorAfter: cursor,
			Overshoot: input.Overshoot, DeltaCount: input.DeltaCount, Applied: applied,
		}
		if input.AdvanceCursor && input.CursorNext != cursor {
			if err := advanceExtractionCursorTx(ctx, tx, input.Source.FrameID, cursor, input.CursorNext); err != nil {
				return err
			}
			result.CursorAfter, result.CursorAdvanced = input.CursorNext, true
		}
		if err := s.insertExtractionReceiptTx(ctx, tx, identity, input.InputSHA256, planDigest, result); err != nil {
			return err
		}
		if input.DeliveryClaim != nil {
			return settleMemoryExtractionClaimTx(ctx, tx, *input.DeliveryClaim, identity.key, "")
		}
		return nil
	})
	if err != nil {
		return ExtractionCommitResult{}, err
	}
	result.DeliverySettled = input.DeliveryClaim != nil
	if result.DeliverySettled {
		s.signalOutboxWake()
	}
	return result, nil
}

// ReadMemoryExtractionReceipt is a read-only replay path. It returns the exact
// committed IDs/outcome without requiring active-branch or fresh-policy grants.
func (s *Store) ReadMemoryExtractionReceipt(ctx context.Context, source CompletedExtractionSource) (ExtractionCommitResult, bool, error) {
	if s == nil || s.db == nil {
		return ExtractionCommitResult{}, false, errors.New("workspace store is closed")
	}
	identity, err := identifyExtractionSource(source)
	if err != nil {
		return ExtractionCommitResult{}, false, err
	}
	database := s.readDB
	if database == nil {
		database = s.db
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ExtractionCommitResult{}, false, err
	}
	defer tx.Rollback()
	if err := requireExtractionFrameTx(ctx, tx, identity); err != nil {
		return ExtractionCommitResult{}, false, err
	}
	saved, found, err := readExtractionReceiptTx(ctx, tx, identity, extractionReceiptReadOnly)
	if err != nil {
		return ExtractionCommitResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ExtractionCommitResult{}, false, err
	}
	if !found {
		return ExtractionCommitResult{}, false, nil
	}
	saved.result.Replayed = true
	return saved.result, true, nil
}

func extractionCursorTx(ctx context.Context, tx memoryTransaction, frameID string) (int, error) {
	var cursor int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(metadata.last_extract_msg_idx,0) FROM frames frame
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id WHERE frame.id=?`, frameID).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("read atomic memory extraction cursor: %w", err)
	}
	if cursor < 0 {
		return 0, ErrExtractionCursorChanged
	}
	return cursor, nil
}

func advanceExtractionCursorTx(ctx context.Context, tx memoryTransaction, frameID string, previous, next int) error {
	updated, err := tx.ExecContext(ctx, `INSERT INTO frame_runtime_metadata(frame_id,last_extract_msg_idx)
		SELECT frame.id,? FROM frames frame WHERE frame.id=?
		ON CONFLICT(frame_id) DO UPDATE SET last_extract_msg_idx=excluded.last_extract_msg_idx
		WHERE COALESCE(frame_runtime_metadata.last_extract_msg_idx,0)=?`, next, frameID, previous)
	if err != nil {
		return fmt.Errorf("advance atomic memory extraction cursor: %w", err)
	}
	if err := requireOneMutationRow(updated, "memory extraction cursor", frameID); err != nil {
		return fmt.Errorf("%w: %w", ErrExtractionCursorChanged, err)
	}
	return nil
}
