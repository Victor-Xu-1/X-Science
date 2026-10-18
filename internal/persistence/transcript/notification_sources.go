package transcript

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NotificationSourcePosition is a per-stream publication coordinate, never a
// global clock. It is only a read projection checkpoint, not delivery authority.
type NotificationSourcePosition struct {
	Publication int64  `json:"publication"`
	Incarnation string `json:"incarnation"`
	Through     int64  `json:"through,omitempty"`
}

type NotificationSource struct {
	SessionID, StreamUID, FrameID, Incarnation, Name string
	ContextJSON                                      []byte
	CreatedAt                                        time.Time
	Through, Scanned                                 int64
	Terminals                                        []TerminalProjection
}

type NotificationSourcePage struct {
	Sources        []NotificationSource
	AfterSession   string
	IncludeSession bool
	Complete       bool
}

// ReadNotificationSources reads at most 16 indexed source heads and 128 event
// metadata rows plus one indexed latest-receipt probe per selected head.
// Canonical payload validation happens only after that bounded selection.
// It never creates a delivery destination, acknowledges an intent, or repairs a
// runner. A caller commits the returned coordinates only with its projection.
func (r *Repository) ReadNotificationSources(ctx context.Context, owner, after string, include bool, positions map[string]NotificationSourcePosition) (NotificationSourcePage, error) {
	result := NotificationSourcePage{Sources: []NotificationSource{}, AfterSession: after, IncludeSession: include}
	if r.readDatabase() == nil || owner == "" {
		return result, ErrSchemaUnavailable
	}
	tx, err := r.readDatabase().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	op := ">"
	if include {
		op = ">="
	}
	rows, err := tx.QueryContext(ctx, `SELECT authority.session_id,stream.stream_uid,stream.frame_id,
		frame.incarnation_id,frame.name,COALESCE(metadata.context_data,'{}'),stream.created_at,
		stream.next_publication_seq-1,COALESCE(activation.target_through_publication_seq,0)
		FROM transcript_frame_authority authority
		JOIN transcript_streams stream ON stream.stream_uid=authority.active_stream_uid AND stream.epoch=authority.active_epoch
		JOIN frames frame ON frame.id=stream.frame_id
		JOIN projects project ON project.id=frame.project_id AND project.user_id=authority.owner_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id
		LEFT JOIN transcript_history_activation_receipts activation ON activation.target_stream_uid=stream.stream_uid
		WHERE authority.owner_id=? AND authority.session_id `+op+` ? AND stream.kind='frame_ref'
		ORDER BY authority.session_id LIMIT 17`, owner, after)
	if err != nil {
		return result, err
	}
	heads := []NotificationSource{}
	for rows.Next() {
		var source NotificationSource
		if err := rows.Scan(&source.SessionID, &source.StreamUID, &source.FrameID, &source.Incarnation, &source.Name,
			&source.ContextJSON, &source.CreatedAt, &source.Through, &source.Scanned); err != nil {
			rows.Close()
			return result, err
		}
		heads = append(heads, source)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	more := len(heads) > 16
	if more {
		heads = heads[:16]
	}
	budget := 128
	historyBlocked := false
	for _, source := range heads {
		// These are the activation lower bound and current upper bound from
		// this same read snapshot, before a frozen history window is restored.
		head := source
		if prior, ok := positions[source.StreamUID]; ok {
			if prior.Incarnation != source.Incarnation { // Never rebind a known stream to a replacement frame.
				if !historyBlocked {
					result.AfterSession, result.IncludeSession = source.SessionID, false
				}
				continue
			}
			if prior.Publication > source.Scanned {
				source.Scanned = prior.Publication
			}
			if prior.Through > prior.Publication {
				if prior.Through > source.Through {
					return result, errors.New("notification source upper bound disappeared")
				}
				source.Through = prior.Through
			}
		}
		if source.Incarnation == "" || source.Scanned > source.Through {
			return result, errors.New("invalid notification source coordinate")
		}
		latest, err := notificationSourceTerminalHead(ctx, tx, owner, head, head.Scanned)
		if err != nil {
			return result, err
		}
		if latest != nil {
			source.Terminals = append(source.Terminals, *latest)
		}
		if !historyBlocked && budget > 0 {
			budget, err = scanNotificationSourceHistory(ctx, tx, owner, &source, budget)
			if err != nil {
				return result, err
			}
		}
		result.Sources = append(result.Sources, source)
		if !historyBlocked {
			result.AfterSession, result.IncludeSession = source.SessionID, source.Scanned < source.Through
		}
		if source.Scanned < source.Through {
			more = true
			historyBlocked = true
		}
	}
	result.Complete = !more
	if result.Complete {
		result.AfterSession, result.IncludeSession = "", false
	}
	return result, tx.Commit()
}
