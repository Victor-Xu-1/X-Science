package transcript

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (r *Repository) framePendingInputRequiresContinueConn(ctx context.Context, conn *sql.Conn, ownerID, frameID string, now time.Time) (bool, error) {
	var streamUID, frameStatus string
	err := conn.QueryRowContext(ctx, `SELECT s.stream_uid,f.status FROM transcript_frame_authority a
		JOIN transcript_streams s ON s.stream_uid=a.active_stream_uid AND s.owner_id=a.owner_id
			AND s.session_id=a.session_id AND s.epoch=a.active_epoch AND s.kind='frame_ref'
		JOIN frames f ON f.id=s.frame_id AND f.project_id=s.project_id
		WHERE a.owner_id=? AND a.session_id=? AND f.id=?`, ownerID, frameID, frameID).Scan(&streamUID, &frameStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || (frameStatus != "completed" && frameStatus != "processing") {
		return false, err
	}
	stream, err := getStreamConn(ctx, conn, streamUID, ownerID)
	if err != nil {
		return false, err
	}
	evidence, err := r.freshMessageEvidenceConn(ctx, conn, stream)
	coverage := evidence.Coverage
	if err != nil || !coverage.Pending() || coverage.Complete || frameStatus == "processing" && evidence.Authorized {
		return false, err
	}
	var live bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_runner_attempts a
		JOIN transcript_streams s ON s.stream_uid=a.stream_uid
		WHERE s.owner_id=? AND s.root_frame_id=? AND a.status='running' AND a.expires_at>?)`,
		ownerID, stream.RootFrameID, now).Scan(&live); err != nil {
		return false, err
	}
	if live {
		return false, nil
	}
	var dispatched bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM frame_events WHERE frame_id=? AND event_type='frame_resumed'
		AND json_extract(payload,'$.dispatch.status') IN ('registered','claimed','blocked'))`, frameID).Scan(&dispatched); err != nil {
		return false, err
	}
	return !dispatched, nil
}

// FramePendingInputRequiresContinue is a derived disposition, never a second
// persisted status. Only absent historical mappings qualify; source conflicts
// remain errors. A later accepted command must recheck it in its transaction.
func (tx *ImmediateTransaction) FramePendingInputRequiresContinue(ctx context.Context, ownerID, frameID string) (bool, error) {
	if tx == nil || tx.repository == nil || tx.conn == nil {
		return false, ErrSchemaUnavailable
	}
	return tx.repository.framePendingInputRequiresContinueConn(ctx, tx.conn, ownerID, frameID, tx.repository.now().UTC())
}

func (r *Repository) FramePendingInputRequiresContinue(ctx context.Context, ownerID, frameID string) (bool, error) {
	if r == nil || r.readDatabase() == nil {
		return false, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return false, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	return r.framePendingInputRequiresContinueConn(ctx, conn, ownerID, frameID, r.now().UTC())
}
