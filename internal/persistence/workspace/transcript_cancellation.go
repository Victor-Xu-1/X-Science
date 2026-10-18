package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

type workspaceTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CancelFrameWithTranscript commits the Frame lifecycle event and canonical
// runner terminal receipt in one SQLite transaction.
func (s *Store) CancelFrameWithTranscript(ctx context.Context, frameID string) (Frame, *FrameEvent, error) {
	if s == nil || s.db == nil {
		return Frame{}, nil, errors.New("workspace store is closed")
	}
	frameID = strings.TrimSpace(frameID)
	if frameID == "" {
		return Frame{}, nil, errors.New("frame id is required")
	}
	repository := transcriptstore.NewRepository(s.db)
	var frame Frame
	var event *FrameEvent
	err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		now := s.now().UTC()
		ownerID, err := frameOwnerInTransaction(ctx, tx, frameID)
		if err != nil {
			return err
		}
		frame, event, err = cancelFrameInTransaction(ctx, tx, frameID, now)
		if err != nil {
			return err
		}
		if err := requestDetachedKernelFrameCancellationTx(ctx, tx, frameID, now); err != nil {
			return err
		}
		_, err = tx.CancelLatestFrameRunner(ctx, ownerID, frameID, "user_cancelled", []string{"ws"})
		return err
	})
	if err != nil {
		return Frame{}, nil, err
	}
	s.signalKernelRetentionWake()
	return frame, event, nil
}

// CancelCompatibilityFrameTreeWithTranscript applies the legacy tree envelope
// while keeping every canonical Transcript terminal in the same transaction.
func (s *Store) CancelCompatibilityFrameTreeWithTranscript(
	ctx context.Context,
	frameID, reason string,
	expectedInputIDs ...string,
) (CancelCompatibilityFrameResult, error) {
	if s == nil || s.db == nil {
		return CancelCompatibilityFrameResult{}, errors.New("workspace store is closed")
	}
	frameID = strings.TrimSpace(frameID)
	if frameID == "" {
		return CancelCompatibilityFrameResult{}, errors.New("frame id is required")
	}
	expectedInput := ""
	if len(expectedInputIDs) > 1 {
		return CancelCompatibilityFrameResult{}, ErrFrameCancellationInputUnavailable
	}
	if len(expectedInputIDs) == 1 {
		expectedInput = strings.TrimSpace(expectedInputIDs[0])
		if expectedInput == "" {
			return CancelCompatibilityFrameResult{}, ErrFrameCancellationInputUnavailable
		}
	}
	repository := transcriptstore.NewRepository(s.db)
	var result CancelCompatibilityFrameResult
	err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		result, err = cancelCompatibilityFrameTreeWithTranscriptTx(ctx, tx, frameID, reason, expectedInput, s.now().UTC())
		return err
	})
	if err != nil {
		return CancelCompatibilityFrameResult{}, err
	}
	s.signalKernelRetentionWake()
	return result, nil
}

func cancelCompatibilityFrameTreeWithTranscriptTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, frameID, reason, expectedInput string, now time.Time) (CancelCompatibilityFrameResult, error) {
	ownerID, err := frameOwnerInTransaction(ctx, tx, frameID)
	if err != nil {
		return CancelCompatibilityFrameResult{}, err
	}
	if expectedInput != "" {
		if err := requireFrameCancellationInputTx(ctx, tx, ownerID, frameID, expectedInput); err != nil {
			return CancelCompatibilityFrameResult{}, err
		}
	}
	result, err := cancelCompatibilityFrameTreeInTransaction(ctx, tx, frameID, reason, now)
	if err != nil {
		return CancelCompatibilityFrameResult{}, err
	}
	for _, cancelledFrameID := range result.CancelledFrameIDs {
		if err := requestDetachedKernelFrameCancellationTx(ctx, tx, cancelledFrameID, now); err != nil {
			return CancelCompatibilityFrameResult{}, err
		}
		if _, err := tx.CancelLatestFrameRunner(ctx, ownerID, cancelledFrameID, "user_cancelled", []string{"ws"}); err != nil {
			return CancelCompatibilityFrameResult{}, err
		}
	}
	return result, nil
}

func frameOwnerInTransaction(ctx context.Context, tx workspaceTransaction, frameID string) (string, error) {
	var ownerID string
	if err := tx.QueryRowContext(ctx, `
		SELECT project.user_id FROM frames frame
		JOIN projects project ON project.id=frame.project_id
		WHERE frame.id=?`, frameID).Scan(&ownerID); err != nil {
		return "", err
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return "", errors.New("frame owner is unavailable")
	}
	return ownerID, nil
}
