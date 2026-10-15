package transcript

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Ephemeral keyset hint only: no admission, lease, or execution authority.
type runnerCandidateCursor struct {
	UpdatedAt string
	UID       string
}

func nextRunnerCandidateConn(ctx context.Context, conn *sql.Conn, includeStandalone bool, now time.Time, cursor runnerCandidateCursor) (Stream, runnerCandidateCursor, bool, error) {
	var stream Stream
	var kind string
	var next runnerCandidateCursor
	err := conn.QueryRowContext(ctx, `
			SELECT stream.stream_uid,stream.owner_id,stream.external_id,stream.session_id,stream.kind,
				stream.project_id,stream.root_frame_id,stream.frame_id,stream.epoch,
				stream.input_revision,stream.consumed_input_revision,stream.next_event_id,
				stream.next_publication_seq,stream.next_checkpoint_sequence,stream.created_at,stream.updated_at,CAST(stream.updated_at AS TEXT)
			FROM transcript_streams stream
			LEFT JOIN frames frame ON frame.id=stream.frame_id AND frame.project_id=stream.project_id
			WHERE stream.input_revision>stream.consumed_input_revision
                AND (?='' OR CAST(stream.updated_at AS TEXT)>? OR (CAST(stream.updated_at AS TEXT)=? AND stream.stream_uid>?))
				AND ((stream.kind='frame_ref' AND frame.id IS NOT NULL
						AND frame.status NOT IN ('completed','failed','cancelled','canceled')
						AND EXISTS (SELECT 1 FROM transcript_frame_authority authority
							WHERE authority.owner_id=stream.owner_id AND authority.session_id=stream.session_id
							AND authority.active_stream_uid=stream.stream_uid AND authority.active_epoch=stream.epoch))
					OR (? AND stream.kind='standalone' AND EXISTS (
						SELECT 1 FROM transcript_delivery_routes route
						WHERE route.stream_uid=stream.stream_uid AND route.destination=stream.session_id
							AND route.status='active'
					)))
				AND NOT EXISTS (
					SELECT 1 FROM transcript_runner_attempts attempt
					WHERE attempt.stream_uid=stream.stream_uid AND attempt.status='running' AND attempt.expires_at>?
				)
				AND (
					NOT EXISTS (
						SELECT 1 FROM transcript_runner_attempts attempt
						WHERE attempt.stream_uid=stream.stream_uid
					)
					OR EXISTS (
						SELECT 1 FROM transcript_runner_attempts latest
						WHERE latest.stream_uid=stream.stream_uid
							AND latest.attempt=(
								SELECT MAX(candidate.attempt) FROM transcript_runner_attempts candidate
								WHERE candidate.stream_uid=stream.stream_uid
							)
							AND (
								(
									latest.claimed_input_revision<stream.input_revision
									AND (`+freshMessageCandidateSQL("stream", "latest")+`
										OR `+pendingRecoveryInputSQL("stream", "latest.claimed_input_revision")+`)
								)
								OR (
									latest.status='running' AND latest.expires_at<=?
									AND EXISTS (
										SELECT 1 FROM transcript_runner_checkpoints checkpoint
										WHERE checkpoint.stream_uid=stream.stream_uid
											AND checkpoint.runner_attempt=latest.attempt
										AND checkpoint.checkpoint_sequence=(
											SELECT MAX(candidate.checkpoint_sequence) FROM transcript_runner_checkpoints candidate
											WHERE candidate.stream_uid=stream.stream_uid AND candidate.runner_attempt=latest.attempt
										)
										AND checkpoint.resumable=1
										AND COALESCE((
											SELECT json_extract(event.payload_json,'$.auto_resume')
											FROM transcript_events event
											WHERE event.stream_uid=checkpoint.stream_uid AND event.event_id=checkpoint.event_id
										),1)=1
									)
									AND (
										latest.claimed_input_revision=stream.input_revision
										OR (
											latest.claimed_input_revision<stream.input_revision
											AND `+pendingInputKindSQL("stream", "latest.claimed_input_revision", false)+`
										)
									)
								)
								OR (
									latest.status='running' AND latest.expires_at<=?
									AND latest.claimed_input_revision=stream.input_revision
									AND `+runnerPreparationProofFor("latest")+`
								)
							)
					)
				)
				AND (
					(
						EXISTS (
							SELECT 1 FROM transcript_runner_attempts latest
							WHERE latest.stream_uid=stream.stream_uid
								AND latest.attempt=(
									SELECT MAX(candidate.attempt) FROM transcript_runner_attempts candidate
									WHERE candidate.stream_uid=stream.stream_uid
								)
								AND latest.claimed_input_revision<stream.input_revision
								AND `+freshMessageCandidateSQL("stream", "latest")+`
						)
					)
					OR NOT EXISTS (
						SELECT 1 FROM frame_events resume
						WHERE resume.frame_id=stream.frame_id AND resume.event_type='frame_resumed'
							AND json_extract(resume.payload, '$.dispatch.status') IN ('registered','claimed','blocked')
					)
				)
			ORDER BY CAST(stream.updated_at AS TEXT),stream.stream_uid LIMIT 1`, cursor.UID, cursor.UpdatedAt, cursor.UpdatedAt, cursor.UID, includeStandalone, now, now, now,
	).Scan(
		&stream.UID, &stream.OwnerID, &stream.ExternalID, &stream.SessionID, &kind,
		&stream.ProjectID, &stream.RootFrameID, &stream.FrameID, &stream.Epoch,
		&stream.InputRevision, &stream.ConsumedInputRevision, &stream.NextEventID,
		&stream.NextPublication, &stream.NextCheckpoint, &stream.CreatedAt, &stream.UpdatedAt, &next.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return stream, next, false, nil
	}
	if err != nil {
		return stream, next, false, err
	}
	stream.Kind = StreamKind(kind)
	next.UID = stream.UID
	return stream, next, true, nil
}
