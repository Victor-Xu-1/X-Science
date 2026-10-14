package workspace

import (
	"context"
	"strconv"
	"strings"
)

func validateFrameContinueTarget(ctx context.Context, tx workspaceTransaction, state frameResumeTargetSnapshot, expected CompatibilityFrameResumeTarget) error {
	if expected.StateRef != state.target.StateRef || resumeTargetString(expected.ResumeEventID) != resumeTargetString(state.target.ResumeEventID) {
		return ErrFrameResumeTargetStale
	}
	oldRef, currentRef := resumeTargetString(expected.WaitRef), resumeTargetString(state.target.WaitRef)
	if oldRef == currentRef {
		return nil
	}
	if oldRef == "" || currentRef == "" || state.dispatch == nil || strings.HasPrefix(oldRef, "legacy-wait:") || strings.HasPrefix(currentRef, "legacy-wait:") {
		return ErrFrameResumeTargetStale
	}
	row := state.dispatch.dispatch
	var stream, checkpoint string
	for _, frame := range state.frames {
		if frame.ID == row.FrameID {
			stream, checkpoint = frame.Stream, frame.Checkpoint
			break
		}
	}
	eventID, err := strconv.ParseInt(checkpoint, 10, 64)
	if err != nil || stream == "" || eventID <= 0 {
		return ErrFrameResumeTargetStale
	}
	var runnerAttempt int
	if err := tx.QueryRowContext(ctx, `SELECT runner_attempt FROM transcript_runner_checkpoints WHERE stream_uid=? AND event_id=?`, stream, eventID).Scan(&runnerAttempt); err != nil {
		return err
	}
	old, found, err := compatibilityDispatchAuditEventByID(ctx, tx, oldRef)
	if err != nil {
		return err
	}
	if !found {
		return ErrFrameResumeTargetStale
	}
	current, found, err := compatibilityDispatchAuditEventByID(ctx, tx, currentRef)
	if err != nil {
		return err
	}
	if !found || old.Sequence <= state.lastWake.Sequence || old.Sequence > current.Sequence {
		return ErrFrameResumeTargetStale
	}
	for _, origin := range []FrameEvent{old, current} {
		if origin.FrameID != row.FrameID || origin.Type != "frame_resume_dispatch_interrupted" ||
			resumeDispatchString(origin.Payload["resumeEventId"]) != row.ResumeEvent.ID ||
			resumeDispatchInt(origin.Payload["runnerAttempt"]) != runnerAttempt ||
			int64(resumeDispatchInt(origin.Payload["checkpointEventId"])) != eventID {
			return ErrFrameResumeTargetStale
		}
	}
	if resumeDispatchString(old.Payload["reasonCode"]) != resumeDispatchString(current.Payload["reasonCode"]) {
		return ErrFrameResumeTargetStale
	}
	return nil
}
