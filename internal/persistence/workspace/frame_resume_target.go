package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type frameResumeTargetFrame struct {
	ID, Incarnation, Owner, Project, Parent, Root, Agent, Status string
	RootSequence                                                 int64
	Stream, Branch                                               string
	Epoch, AuthorityGeneration, InputRevision, BranchGeneration  int64
	Checkpoint, LegacyInput                                      string
}

type frameResumeTargetSnapshot struct {
	target   CompatibilityFrameResumeTarget
	frames   []frameResumeTargetFrame
	dispatch *compatibilityFrameResumeDispatchRow
	lastWake FrameEvent
}

// GetCompatibilityFrameResumeTarget reads existing authorities in one snapshot.
// It does not reserve a runner, create a command, or use a wall-clock boundary.
func (s *Store) GetCompatibilityFrameResumeTarget(ctx context.Context, ownerID, rootID string) (CompatibilityFrameResumeTarget, error) {
	if s == nil || s.db == nil || ctx == nil {
		return CompatibilityFrameResumeTarget{}, errors.New("resume target store/context is unavailable")
	}
	db := s.readDB
	if db == nil {
		db = s.db
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CompatibilityFrameResumeTarget{}, err
	}
	defer tx.Rollback()
	state, err := frameResumeTargetTx(ctx, tx, strings.TrimSpace(ownerID), strings.TrimSpace(rootID))
	if err != nil {
		return CompatibilityFrameResumeTarget{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompatibilityFrameResumeTarget{}, err
	}
	return state.target, nil
}

func frameResumeTargetTx(ctx context.Context, tx workspaceTransaction, ownerID, rootID string) (frameResumeTargetSnapshot, error) {
	var state frameResumeTargetSnapshot
	owner, err := frameOwnerInTransaction(ctx, tx, rootID)
	if err != nil {
		return state, err
	}
	if ownerID == "" || owner != ownerID {
		return state, errors.New("resume target owner does not match")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT f.id,f.incarnation_id,p.user_id,f.project_id,COALESCE(f.parent_frame_id,''),
			f.root_frame_id,f.agent_name,f.status,f.root_sequence,
			COALESCE(s.stream_uid,''),COALESCE(s.epoch,0),COALESCE(a.authority_generation,0),
			COALESCE(s.input_revision,0),COALESCE(b.active_branch_id,''),COALESCE(b.generation,0),
			COALESCE((SELECT CAST(c.event_id AS TEXT) FROM transcript_branch_events be
				JOIN transcript_runner_checkpoints c ON c.stream_uid=be.stream_uid AND c.event_id=be.event_id
				WHERE be.stream_uid=s.stream_uid AND be.branch_id=b.active_branch_id ORDER BY be.ordinal DESC LIMIT 1),''),
			COALESCE((SELECT e.id FROM frame_events e WHERE e.frame_id=f.id AND
				(e.event_type IN ('user_message','queued_message') OR
				(e.event_type='message' AND json_extract(e.payload,'$.role')='user'))
				ORDER BY e.sequence DESC LIMIT 1),'')
		FROM frames f JOIN projects p ON p.id=f.project_id
		LEFT JOIN transcript_frame_authority a ON a.owner_id=p.user_id AND a.session_id=f.id
		LEFT JOIN transcript_streams s ON s.stream_uid=a.active_stream_uid AND s.owner_id=a.owner_id
			AND s.epoch=a.active_epoch AND s.kind='frame_ref' AND s.frame_id=f.id
		LEFT JOIN transcript_branch_state b ON b.stream_uid=s.stream_uid
		WHERE f.root_frame_id=? ORDER BY f.root_sequence,f.id`, rootID)
	if err != nil {
		return state, err
	}
	rootFound := false
	for rows.Next() {
		var frame frameResumeTargetFrame
		if err := rows.Scan(&frame.ID, &frame.Incarnation, &frame.Owner, &frame.Project, &frame.Parent,
			&frame.Root, &frame.Agent, &frame.Status, &frame.RootSequence,
			&frame.Stream, &frame.Epoch, &frame.AuthorityGeneration, &frame.InputRevision, &frame.Branch,
			&frame.BranchGeneration, &frame.Checkpoint, &frame.LegacyInput); err != nil {
			_ = rows.Close()
			return state, err
		}
		if frame.Stream != "" {
			frame.LegacyInput = ""
		}
		rootFound = rootFound || frame.ID == rootID && frame.Parent == ""
		state.frames = append(state.frames, frame)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return state, err
	}
	if !rootFound {
		return state, errors.New("resume target requires a root frame")
	}
	row, found, err := scanCompatibilityFrameResumeDispatch(tx.QueryRowContext(ctx, `
		SELECT e.id,e.frame_id,e.sequence,e.payload,e.created_at,f.root_frame_id,f.project_id,f.agent_name,f.status
		FROM frame_events e JOIN frames f ON f.id=e.frame_id
		WHERE f.root_frame_id=? AND e.event_type='frame_resumed'
			AND json_extract(e.payload,'$.dispatch.status') IS NOT NULL
		ORDER BY CASE WHEN f.status IN ('processing','running') AND
			json_extract(e.payload,'$.dispatch.status') IN ('registered','claimed','blocked') THEN 0 ELSE 1 END,
			e.created_at DESC,e.id DESC LIMIT 1`, rootID))
	if err != nil {
		return state, err
	}
	dispatchState := "none"
	if found {
		state.dispatch = &row
		state.target.ResumeEventID = &row.dispatch.ResumeEvent.ID
		var wakeID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM frame_events WHERE frame_id=?
			AND event_type='frame_resume_dispatch_woken' AND json_extract(payload,'$.resumeEventId')=?
			ORDER BY sequence DESC LIMIT 1`, row.dispatch.FrameID, row.dispatch.ResumeEvent.ID).Scan(&wakeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return state, err
		}
		if wakeID != "" {
			state.lastWake, _, err = compatibilityDispatchAuditEventByID(ctx, tx, wakeID)
			if err != nil {
				return state, err
			}
		}
		dispatchState = row.dispatch.Status
		if !isCompatibilityResumeDispatchTerminal(dispatchState) {
			dispatchState = "active"
			payload := mapValueForResumeTarget(row.dispatch.ResumeEvent.Payload["dispatch"])
			_, timeFenced := payload["notBefore"]
			waiting := timeFenced || row.dispatch.WaitingFor != ""
			if !waiting && row.dispatch.Status == frameResumeDispatchClaimed && resumeDispatchString(payload["waitEventId"]) != "" {
				_, woken, err := compatibilityDispatchAuditEventByID(ctx, tx, frameResumeWakeAuditID(row.dispatch.ResumeEvent.ID, row.dispatch.WaitRef))
				if err != nil {
					return state, err
				}
				waiting = !woken
			}
			if waiting {
				dispatchState = "waiting"
				state.target.WaitRef = &row.dispatch.WaitRef
			}
		}
	}
	raw, err := json.Marshal(struct {
		Frames              []frameResumeTargetFrame
		Resume, State, Wake string
	}{state.frames, resumeTargetString(state.target.ResumeEventID), dispatchState, state.lastWake.ID})
	if err != nil {
		return state, err
	}
	digest := sha256.Sum256(append([]byte("x-science.continue-target\x00"), raw...))
	state.target.StateRef = "resume-state:" + hex.EncodeToString(digest[:])
	return state, nil
}

func resumeTargetString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapValueForResumeTarget(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}
