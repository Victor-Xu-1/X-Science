package transcript

import (
	"context"
	"database/sql"
)

// EnsureHistoricalInputNotice uses only the existing immediate transaction and
// event/delivery receipt authority. Its key excludes its own publication and
// all polling/delivery timestamps. Repetition cannot create another notice.
func (r *Repository) EnsureHistoricalInputNotice(ctx context.Context, owner, destination string) (bool, error) {
	var created bool
	err := r.withImmediate(ctx, func(conn *sql.Conn) error {
		var uid sql.NullString
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT MIN(stream_uid),COUNT(*) FROM transcript_streams WHERE owner_id=? AND session_id=?`, owner, destination).Scan(&uid, &count); err != nil {
			return err
		}
		if count != 1 || !uid.Valid {
			return ErrEventConflict
		}
		stream, err := getStreamConn(ctx, conn, uid.String, owner)
		if err != nil {
			return err
		}
		state, _, eligible, err := r.historicalInputNoticeStateConn(ctx, conn, stream, destination)
		if err != nil || !eligible {
			return err
		}
		payload, identity, err := state.record()
		if err != nil {
			return err
		}
		_, created, err = appendEventConn(ctx, conn, stream, eventRecord{clientMessageID: identity,
			eventType: HistoricalInputNoticeEventType, source: EventSourcePayload, payloadJSON: payload,
			destinations: []string{destination}, createdAt: r.now().UTC()})
		return err
	})
	return created, schemaError(err)
}

// HistoricalInputNoticeCurrent is a read-only, exact-claim send guard. The text
// returned is from a verified user source, for the existing language detector
// only. It is never an instruction or an execution/recovery permission.
func (r *Repository) HistoricalInputNoticeCurrent(ctx context.Context, claim DeliveryClaim) (string, bool, error) {
	if err := validateDeliveryClaim(claim); err != nil {
		return "", false, err
	}
	if r.readDatabase() == nil {
		return "", false, ErrSchemaUnavailable
	}
	conn, err := r.readDatabase().Conn(ctx)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", false, err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	stored, err := loadDeliveryClaimState(ctx, conn, claim)
	if err != nil {
		return "", false, err
	}
	if stored.status != "inflight" || !stored.leaseExpires.Valid || !stored.leaseExpires.Time.After(r.now().UTC()) {
		return "", false, ErrDeliveryClaimStale
	}
	stream, err := getStreamConn(ctx, conn, claim.StreamUID, claim.OwnerID)
	if err != nil {
		return "", false, err
	}
	state, text, eligible, err := r.historicalInputNoticeStateConn(ctx, conn, stream, claim.Destination)
	if err != nil || !eligible || state.RouteGeneration != claim.RouteGeneration {
		return "", false, err
	}
	payload, identity, err := state.record()
	if err != nil {
		return "", false, err
	}
	event, found, err := findEventByClientID(ctx, conn, stream.UID, identity)
	if err != nil || !found {
		return "", false, err
	}
	if event.EventID != claim.Event.EventID || event.PublicationSeq != claim.PublicationSeq {
		return "", false, nil // The queued notice describes a superseded state.
	}
	if event.Type != HistoricalInputNoticeEventType || event.Source != EventSourcePayload || event.RunnerAttempt != nil ||
		event.FrameEventID != nil || !equalBytes(payload, event.PayloadJSON) || !equalBytes(event.PayloadJSON, claim.ResolvedPayloadJSON) {
		return "", false, ErrEventConflict
	}
	return text, true, nil
}
