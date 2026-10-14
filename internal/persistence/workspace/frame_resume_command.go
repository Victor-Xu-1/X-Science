package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

type frameContinueRecord struct {
	RequestSHA256 string                            `json:"requestSHA256"`
	ActorID       string                            `json:"actorId"`
	ResumeEventID string                            `json:"resumeEventId"`
	WaitRef       string                            `json:"waitRef"`
	Source        string                            `json:"source"`
	RootFrameID   string                            `json:"rootFrameId"`
	FrameID       string                            `json:"frameId"`
	AgentName     string                            `json:"agentName"`
	Leaves        int                               `json:"leaves"`
	Receipt       CompatibilityFrameContinueReceipt `json:"receipt"`
}

func frameContinueRequestDigest(input ResumeCompatibilityFrameInput) (string, error) {
	command := input.Command
	if command == nil || command.ActorID == "" || command.ClientCommandID == "" || command.Target.StateRef == "" {
		return "", ErrFrameResumePrecondition
	}
	if _, err := uuid.Parse(command.ClientCommandID); err != nil {
		return "", ErrFrameResumePrecondition
	}
	if len(command.ClientCommandID) != 36 || !strings.HasPrefix(command.Target.StateRef, "resume-state:") || len(command.Target.StateRef) != 77 {
		return "", ErrFrameResumePrecondition
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(command.Target.StateRef, "resume-state:")); err != nil {
		return "", ErrFrameResumePrecondition
	}
	raw, err := json.Marshal(struct {
		Target   CompatibilityFrameResumeTarget
		Controls map[string]any
	}{command.Target, compatibilityResumeControlPayload(input)})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func frameContinueCommandIdentity(ctx context.Context, tx workspaceTransaction, rootID string, command *CompatibilityFrameContinueCommand) (string, error) {
	var owner, incarnation string
	if err := tx.QueryRowContext(ctx, `SELECT p.user_id,f.incarnation_id FROM frames f
		JOIN projects p ON p.id=f.project_id WHERE f.id=? AND f.root_frame_id=f.id AND f.parent_frame_id IS NULL`, rootID).Scan(&owner, &incarnation); err != nil {
		return "", err
	}
	if owner != command.ActorID {
		return "", errors.New("continue command actor does not own the root frame")
	}
	raw, _ := json.Marshal([]string{owner, rootID, incarnation, command.ClientCommandID})
	digest := sha256.Sum256(append([]byte("x-science.continue-command\x00"), raw...))
	return hex.EncodeToString(digest[:]), nil
}

func frameContinueReplayTx(ctx context.Context, tx workspaceTransaction, rootID string, input ResumeCompatibilityFrameInput, digest string) (ResumeCompatibilityFrameResult, bool, error) {
	key, err := frameContinueCommandIdentity(ctx, tx, rootID, input.Command)
	if err != nil {
		return ResumeCompatibilityFrameResult{}, false, err
	}
	event, found, err := compatibilityDispatchAuditEventByID(ctx, tx, "frame-resume-continue-command:"+key)
	if err != nil || !found {
		return ResumeCompatibilityFrameResult{}, false, err
	}
	var record frameContinueRecord
	raw, _ := json.Marshal(event.Payload)
	if json.Unmarshal(raw, &record) != nil || event.Type != "frame_resume_continue_command_accepted" ||
		event.FrameID != record.FrameID || record.ActorID != input.Command.ActorID || record.RequestSHA256 != digest ||
		record.RootFrameID != rootID || record.Receipt.ID != event.ID || record.ResumeEventID == "" {
		return ResumeCompatibilityFrameResult{}, false, ErrFrameResumeCommandUsed
	}
	record.Receipt.Replayed = true
	result := ResumeCompatibilityFrameResult{RootFrameID: record.RootFrameID, ResumedFrameID: record.FrameID,
		AgentName: record.AgentName, LeafFramesReady: record.Leaves, ContinueCommand: &record.Receipt,
		Event: &FrameEvent{ID: record.ResumeEventID, FrameID: record.FrameID, Type: "frame_resumed"}}
	dispatch, found, err := scanCompatibilityFrameResumeDispatch(tx.QueryRowContext(ctx, `
		SELECT e.id,e.frame_id,e.sequence,e.payload,e.created_at,f.root_frame_id,f.project_id,f.agent_name,f.status
		FROM frame_events e JOIN frames f ON f.id=e.frame_id WHERE e.id=? AND e.event_type='frame_resumed'`, record.ResumeEventID))
	if err != nil || !found || dispatch.dispatch.FrameID != record.FrameID {
		return ResumeCompatibilityFrameResult{}, false, errors.Join(errors.New("accepted continue dispatch unavailable"), err)
	}
	result.Event = &dispatch.dispatch.ResumeEvent
	payload := mapValueForResumeTarget(result.Event.Payload["dispatch"])
	_, fenced := payload["notBefore"]
	if dispatch.dispatch.Status == frameResumeDispatchRegistered && !fenced && dispatch.dispatch.WaitingFor == "" {
		result.NotifyDispatch = record.Receipt.Outcome == "resumed" && dispatch.dispatch.Attempt == 0 ||
			record.Receipt.Outcome == "woken" && dispatch.dispatch.WaitRef == record.WaitRef
	}
	if record.Receipt.WakeEventID != "" {
		wake, found, err := compatibilityDispatchAuditEventByID(ctx, tx, record.Receipt.WakeEventID)
		if err != nil || !found {
			return ResumeCompatibilityFrameResult{}, false, errors.Join(errors.New("accepted continue wake receipt unavailable"), err)
		}
		result.WakeEvent = &wake
	}
	return result, true, nil
}

// The HTTP layer uses this same receipt reader before dynamic model/agent
// validation. The writing transaction repeats it before any state mutation.
func (s *Store) LookupCompatibilityFrameContinueCommand(ctx context.Context, rootID string, input ResumeCompatibilityFrameInput) (ResumeCompatibilityFrameResult, bool, error) {
	digest, err := frameContinueRequestDigest(input)
	if err != nil {
		return ResumeCompatibilityFrameResult{}, false, err
	}
	if s == nil || s.db == nil || ctx == nil {
		return ResumeCompatibilityFrameResult{}, false, errors.New("continue command store/context unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ResumeCompatibilityFrameResult{}, false, err
	}
	defer tx.Rollback()
	return frameContinueReplayTx(ctx, tx, strings.TrimSpace(rootID), input, digest)
}

func (s *Store) resumeCompatibilityFrameCommand(ctx context.Context, rootID string, input ResumeCompatibilityFrameInput) (ResumeCompatibilityFrameResult, error) {
	digest, err := frameContinueRequestDigest(input)
	if err != nil {
		return ResumeCompatibilityFrameResult{}, err
	}
	var result ResumeCompatibilityFrameResult
	err = transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		replay, found, err := frameContinueReplayTx(ctx, tx, rootID, input, digest)
		if err != nil || found {
			result = replay
			return err
		}
		state, err := frameResumeTargetTx(ctx, tx, input.Command.ActorID, rootID)
		if err != nil {
			return err
		}
		if err := validateFrameContinueTarget(ctx, tx, state, input.Command.Target); err != nil {
			return err
		}
		if state.dispatch != nil && state.dispatch.dispatch.Status == frameResumeDispatchClaimed {
			return ErrFrameResumeCommandBusy
		}
		now := s.now().UTC()
		result, err = resumeCompatibilityFrameConversationTx(ctx, tx, rootID, input, now)
		if err != nil {
			return err
		}
		key, err := frameContinueCommandIdentity(ctx, tx, rootID, input.Command)
		if err != nil {
			return err
		}
		receipt := CompatibilityFrameContinueReceipt{ID: "frame-resume-continue-command:" + key, Target: input.Command.Target,
			ResumeEventID: result.Event.ID, Outcome: "resumed"}
		waitRef := ""
		if state.dispatch != nil && result.Event.ID == state.dispatch.dispatch.ResumeEvent.ID {
			receipt.Outcome = "already_running"
			if state.target.WaitRef != nil {
				receipt.Outcome, waitRef = "woken", *state.target.WaitRef
				receipt.WakeEventID = frameResumeWakeAuditID(result.Event.ID, waitRef)
			}
		}
		record := frameContinueRecord{RequestSHA256: digest, ActorID: input.Command.ActorID, ResumeEventID: result.Event.ID,
			WaitRef: waitRef, Source: string(FrameResumeWakeContinue), RootFrameID: rootID, FrameID: result.ResumedFrameID,
			AgentName: result.AgentName, Leaves: result.LeafFramesReady, Receipt: receipt}
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		if _, err := appendCompatibilityDispatchAuditEvent(ctx, tx, receipt.ID, result.ResumedFrameID, "frame_resume_continue_command_accepted", payload, now); err != nil {
			return err
		}
		if waitRef != "" {
			wake, changed, err := wakeCompatibilityFrameResumeDispatchTx(ctx, tx, FrameResumeDispatchWakeInput{
				ResumeEventID: result.Event.ID, ExpectedWaitRef: waitRef, ActorID: input.Command.ActorID, Source: FrameResumeWakeContinue,
				Cause: FrameResumeWakeCause{Kind: "continue_command", ReferenceID: key, Evidence: "observed"},
			}, now)
			if err != nil {
				return err
			}
			if !changed {
				return ErrFrameResumeTargetStale
			}
			result.WakeEvent = &wake
		}
		result.ContinueCommand = &receipt
		result.NotifyDispatch = receipt.Outcome != "already_running"
		return nil
	})
	return result, err
}
