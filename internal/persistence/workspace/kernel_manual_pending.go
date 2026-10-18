package workspace

import (
	"context"
	"database/sql"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

const manualKernelPendingWindow = 128
const manualKernelPendingLimit = 32

// LIMIT bounds metadata before any payload decoding or state filtering. The
// extra row is only lookahead; payloads are read by their existing primary key.
const manualKernelPendingWindowSQL = `SELECT id,sequence,event_type FROM frame_events
	WHERE frame_id=? AND sequence<=? AND sequence<? ORDER BY sequence DESC LIMIT ?`

type ManualKernelPendingPage struct {
	Records         []ManualKernelExecution
	NextCursor      string
	ScanComplete    bool
	ThroughSequence int64
}

type manualKernelPendingEvent struct {
	id        string
	sequence  int64
	eventType string
}

// ListManualKernelPending discovers receipts without observing or changing the
// Manager. Completion describes this frozen history bound, not in-flight POSTs.
func (s *Store) ListManualKernelPending(ctx context.Context, access KernelFrameAccess, cursor string) (ManualKernelPendingPage, error) {
	if s == nil || s.db == nil {
		return ManualKernelPendingPage{}, ErrWorkspaceStoreClosed
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManualKernelPendingPage{}, err
	}
	defer tx.Rollback()
	binding := manualKernelAccessBinding(access)
	if err := validateManualKernelScope(ctx, tx, binding); err != nil {
		return ManualKernelPendingPage{}, err
	}
	var hidden bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT is_hidden FROM frame_runtime_metadata WHERE frame_id=?),0)`, binding.FrameID).Scan(&hidden); err != nil {
		return ManualKernelPendingPage{}, err
	}
	if hidden {
		return ManualKernelPendingPage{}, transcriptstore.ErrOwnerMismatch
	}
	position, err := manualKernelPendingPosition(ctx, tx, binding, cursor)
	if err != nil {
		return ManualKernelPendingPage{}, err
	}
	metadata, err := readManualKernelPendingWindow(ctx, tx, binding.FrameID, position)
	if err != nil {
		return ManualKernelPendingPage{}, err
	}
	page := ManualKernelPendingPage{Records: make([]ManualKernelExecution, 0), ThroughSequence: position.Through, ScanComplete: true}
	scanned := 0
	for _, event := range metadata[:min(len(metadata), manualKernelPendingWindow)] {
		scanned++
		position.Before = event.sequence
		if event.eventType == ManualKernelExecutionEventType {
			id := strings.TrimPrefix(event.id, "kernel-manual:")
			record, _, found, err := readManualKernelExecution(ctx, tx, id)
			if err != nil {
				return ManualKernelPendingPage{}, err
			}
			if !found || event.id != manualKernelEventID(record.ExecutionID) {
				return ManualKernelPendingPage{}, ErrManualKernelExecutionInvalid
			}
			if manualKernelAccessMatches(record.ManualKernelBinding, binding) && record.State != "terminal" {
				page.Records = append(page.Records, record)
			}
		}
		if len(page.Records) == manualKernelPendingLimit {
			break
		}
	}
	if scanned < len(metadata) {
		page.ScanComplete = false
		page.NextCursor, err = encodeManualKernelPendingCursor(position)
		if err != nil {
			return ManualKernelPendingPage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ManualKernelPendingPage{}, err
	}
	return page, nil
}

func manualKernelPendingPosition(ctx context.Context, tx workspaceTransaction, binding ManualKernelBinding, cursor string) (manualKernelPendingCursor, error) {
	if cursor != "" {
		return decodeManualKernelPendingCursor(cursor, binding)
	}
	var through int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM frame_events WHERE frame_id=?`, binding.FrameID).Scan(&through); err != nil {
		return manualKernelPendingCursor{}, err
	}
	if through < 0 || through >= 9007199254740991 {
		return manualKernelPendingCursor{}, ErrManualKernelExecutionInvalid
	}
	return newManualKernelPendingCursor(binding, through, through+1), nil
}

func readManualKernelPendingWindow(ctx context.Context, tx workspaceTransaction, frameID string, cursor manualKernelPendingCursor) ([]manualKernelPendingEvent, error) {
	rows, err := tx.QueryContext(ctx, manualKernelPendingWindowSQL, frameID, cursor.Through, cursor.Before, manualKernelPendingWindow+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]manualKernelPendingEvent, 0, manualKernelPendingWindow+1)
	for rows.Next() {
		var event manualKernelPendingEvent
		if err := rows.Scan(&event.id, &event.sequence, &event.eventType); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
