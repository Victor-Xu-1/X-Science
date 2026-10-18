package transcript

import (
	"context"
	"database/sql"
	"errors"
)

// Read the latest receipt before considering its status. Filtering terminal
// status before LIMIT would scan arbitrarily far backwards past cancelled or
// reclaimed attempts. The existing (stream_uid, attempt) primary key bounds
// this probe; no delivery ACK, new index, or execution state is involved.
func notificationSourceTerminalHead(ctx context.Context, tx *sql.Tx, owner string, source NotificationSource, activationThrough int64) (*TerminalProjection, error) {
	var eventID int64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT event_id,status FROM transcript_runner_receipts
		WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, source.StreamUID).Scan(&eventID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status != "completed" && status != "failed" {
		return nil, nil
	}
	projection, err := terminalProjectionHeader(ctx, tx, owner, source.StreamUID, eventID)
	if err != nil {
		return nil, err
	}
	if projection.FrameID != source.FrameID || projection.PublicationSeq > source.Through || projection.TerminalStatus != status {
		return nil, ErrEventConflict
	}
	if projection.Purpose == ContextCompactionPurpose || projection.Superseded || projection.PublicationSeq <= activationThrough {
		return nil, nil
	}
	return &projection, nil
}

// This is the historical 128-row budget only. A latest-head discovery must
// never move this cursor past any unvisited event, even if its outcome is new.
func scanNotificationSourceHistory(ctx context.Context, tx *sql.Tx, owner string, source *NotificationSource, budget int) (int, error) {
	type eventMeta struct {
		id, publication int64
		kind            string
	}
	events := []eventMeta{}
	rows, err := tx.QueryContext(ctx, `SELECT event_id,publication_seq,event_type FROM transcript_events
		WHERE stream_uid=? AND publication_seq>? AND publication_seq<=? ORDER BY publication_seq LIMIT ?`,
		source.StreamUID, source.Scanned, source.Through, budget)
	if err != nil {
		return budget, err
	}
	for rows.Next() {
		var event eventMeta
		if err := rows.Scan(&event.id, &event.publication, &event.kind); err != nil {
			rows.Close()
			return budget, err
		}
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		return budget, err
	}
	if err := rows.Err(); err != nil {
		return budget, err
	}
	for _, event := range events {
		if event.kind == "runner_finished" {
			projection, err := terminalProjectionHeader(ctx, tx, owner, source.StreamUID, event.id)
			if err != nil {
				return budget, err
			}
			known := false
			for _, terminal := range source.Terminals {
				known = known || terminal.EventID == event.id
			}
			if !known && !projection.Superseded && projection.Purpose != ContextCompactionPurpose {
				source.Terminals = append(source.Terminals, projection)
			}
		}
		source.Scanned = event.publication
	}
	return budget - len(events), nil
}
