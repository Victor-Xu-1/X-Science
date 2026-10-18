package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	transcriptstore "x-science/internal/persistence/transcript"
)

const DefaultKernelDelegationSpawnCap = 1000

type KernelDelegateRequest struct {
	Task           string
	Name           string
	ContextSummary string
	Profile        string
	OutputSchema   map[string]any
	Model          string
}

type CreateKernelDelegatesInput struct {
	Admission     *KernelDelegationAdmission
	ParentFrameID string
	OwnerUserID   string
	ToolUseID     string
	Requests      []KernelDelegateRequest
	SpawnCap      int
	MaxDepth      int
}

type KernelSupervisedChild struct {
	FrameID        string         `json:"frame_id"`
	ParentFrameID  string         `json:"parent_frame_id"`
	RootFrameID    string         `json:"root_frame_id"`
	ProjectID      string         `json:"project_id"`
	OwnerUserID    string         `json:"owner_user_id"`
	ToolUseID      string         `json:"tool_use_id"`
	AgentName      string         `json:"agent_name"`
	Name           string         `json:"name,omitempty"`
	Task           string         `json:"task"`
	ContextSummary string         `json:"context_summary,omitempty"`
	Model          string         `json:"model,omitempty"`
	OutputSchema   map[string]any `json:"output_schema,omitempty"`
	Status         string         `json:"status"`
	Output         map[string]any `json:"output_data,omitempty"`
	Error          string         `json:"error,omitempty"`
	Dispatched     bool           `json:"dispatched"`
	StartedAt      time.Time      `json:"started_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
}

type KernelDelegationStats struct {
	SpawnedThisTask int `json:"spawned_this_task"`
	Cap             int `json:"cap"`
	Remaining       int `json:"remaining"`
}

func (s *Store) ensureKernelSupervisionSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("workspace store is closed")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS kernel_child_supervision (
			frame_id TEXT PRIMARY KEY REFERENCES frames(id) ON DELETE CASCADE,
			parent_frame_id TEXT NOT NULL REFERENCES frames(id) ON DELETE CASCADE,
			root_frame_id TEXT NOT NULL REFERENCES frames(id) ON DELETE CASCADE,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			owner_user_id TEXT NOT NULL,
			tool_use_id TEXT NOT NULL,
			agent_name TEXT NOT NULL,
			delegate_name TEXT NOT NULL DEFAULT '',
			task TEXT NOT NULL,
			context_summary TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			output_schema TEXT NOT NULL DEFAULT 'null',
			status TEXT NOT NULL,
			output_data TEXT NOT NULL DEFAULT 'null',
			error TEXT NOT NULL DEFAULT '',
			dispatched INTEGER NOT NULL DEFAULT 0,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS kernel_child_parent_status_idx
			ON kernel_child_supervision(parent_frame_id, status, started_at, frame_id)`,
		`CREATE INDEX IF NOT EXISTS kernel_child_root_spawn_idx
			ON kernel_child_supervision(root_frame_id, started_at, frame_id)`,
		`CREATE TABLE IF NOT EXISTS kernel_child_message_clock (
			frame_id TEXT PRIMARY KEY REFERENCES kernel_child_supervision(frame_id) ON DELETE CASCADE,
			enqueued_generation INTEGER NOT NULL DEFAULT 0,
			consumed_generation INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS kernel_child_messages (
			id TEXT PRIMARY KEY,
			frame_id TEXT NOT NULL REFERENCES kernel_child_supervision(frame_id) ON DELETE CASCADE,
			generation INTEGER NOT NULL,
			sender_frame_id TEXT NOT NULL REFERENCES frames(id) ON DELETE CASCADE,
			message TEXT NOT NULL,
			kind TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			consumed_at TIMESTAMP,
			UNIQUE(frame_id, generation)
		)`,
		`CREATE INDEX IF NOT EXISTS kernel_child_messages_pending_idx
			ON kernel_child_messages(frame_id, consumed_at, generation)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prepare kernel supervision schema: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateKernelSupervisedChildren(ctx context.Context, input CreateKernelDelegatesInput) ([]KernelSupervisedChild, error) {
	if ctx == nil {
		return nil, errors.New("kernel delegation context is required")
	}
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return nil, err
	}
	input.ParentFrameID = strings.TrimSpace(input.ParentFrameID)
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.ToolUseID = strings.TrimSpace(input.ToolUseID)
	if input.ParentFrameID == "" || input.OwnerUserID == "" || input.ToolUseID == "" {
		return nil, errors.New("parent frame, owner, and tool use id are required")
	}
	if len(input.Requests) == 0 {
		return []KernelSupervisedChild{}, nil
	}
	if input.SpawnCap <= 0 {
		input.SpawnCap = DefaultKernelDelegationSpawnCap
	}
	if input.MaxDepth <= 0 {
		input.MaxDepth = 1
	}
	if input.MaxDepth > 2 {
		input.MaxDepth = 2
	}
	var children []KernelSupervisedChild
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		var err error
		children, err = s.createKernelSupervisedChildrenTx(ctx, tx, input)
		return err
	})
	return children, err
}

func appendKernelSupervisionFrameEvent(ctx context.Context, tx workspaceTransaction, input FrameEventInput, now time.Time) (FrameEvent, error) {
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return FrameEvent{}, err
	}
	existing, storedPayload, found, err := frameEventByID(ctx, tx, input.ID)
	if err != nil {
		return FrameEvent{}, err
	}
	if found {
		if existing.FrameID != input.FrameID || existing.Type != input.Type || storedPayload != string(raw) {
			return FrameEvent{}, fmt.Errorf("frame event id %q already identifies a different frame event", input.ID)
		}
		return existing, nil
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM frame_events WHERE frame_id = ?`, input.FrameID).Scan(&sequence); err != nil {
		return FrameEvent{}, err
	}
	event := FrameEvent{ID: input.ID, FrameID: input.FrameID, Sequence: sequence, Type: input.Type, Payload: payload, CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO frame_events (id, frame_id, sequence, event_type, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		event.ID, event.FrameID, event.Sequence, event.Type, string(raw), event.CreatedAt); err != nil {
		return FrameEvent{}, err
	}
	return event, nil
}

func (s *Store) MarkKernelChildDispatched(ctx context.Context, frameID, ownerUserID string) error {
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE kernel_child_supervision SET dispatched = 1 WHERE frame_id = ? AND owner_user_id = ?`, strings.TrimSpace(frameID), strings.TrimSpace(ownerUserID))
	if err != nil {
		return fmt.Errorf("mark child dispatched: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errors.New("delegated child is unavailable")
	}
	return nil
}

func (s *Store) GetKernelSupervisedChild(ctx context.Context, parentFrameID, frameID, ownerUserID string) (KernelSupervisedChild, bool, error) {
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return KernelSupervisedChild{}, false, err
	}
	return scanKernelSupervisedChild(s.db.QueryRowContext(ctx, `
		SELECT frame_id, parent_frame_id, root_frame_id, project_id, owner_user_id, tool_use_id,
			agent_name, delegate_name, task, context_summary, model, output_schema, status,
			output_data, error, dispatched, started_at, completed_at
		FROM kernel_child_supervision WHERE frame_id = ? AND parent_frame_id = ? AND owner_user_id = ?`,
		strings.TrimSpace(frameID), strings.TrimSpace(parentFrameID), strings.TrimSpace(ownerUserID)))
}

func (s *Store) ListKernelActiveChildren(ctx context.Context, parentFrameID, ownerUserID string) ([]KernelSupervisedChild, error) {
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT frame_id, parent_frame_id, root_frame_id, project_id, owner_user_id, tool_use_id,
			agent_name, delegate_name, task, context_summary, model, output_schema, status,
			output_data, error, dispatched, started_at, completed_at
		FROM kernel_child_supervision
		WHERE parent_frame_id = ? AND owner_user_id = ? AND status IN ('processing','awaiting_user_response')
		ORDER BY started_at, frame_id`, strings.TrimSpace(parentFrameID), strings.TrimSpace(ownerUserID))
	if err != nil {
		return nil, fmt.Errorf("list active delegated children: %w", err)
	}
	defer rows.Close()
	children := []KernelSupervisedChild{}
	for rows.Next() {
		child, _, err := scanKernelSupervisedChild(rows)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, rows.Err()
}

func (s *Store) KernelDelegationStats(ctx context.Context, frameID, ownerUserID string, cap int) (KernelDelegationStats, error) {
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return KernelDelegationStats{}, err
	}
	if cap <= 0 {
		cap = DefaultKernelDelegationSpawnCap
	}
	var rootFrameID, owner string
	if err := s.db.QueryRowContext(ctx, `SELECT f.root_frame_id, p.user_id FROM frames f JOIN projects p ON p.id=f.project_id WHERE f.id=?`, frameID).Scan(&rootFrameID, &owner); err != nil || owner != ownerUserID {
		return KernelDelegationStats{}, errors.New("kernel delegation frame is unavailable")
	}
	var spawned int
	var childRecordCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_child_supervision WHERE frame_id=? AND owner_user_id=?`, frameID, ownerUserID).Scan(&childRecordCount); err != nil {
		return KernelDelegationStats{}, err
	}
	countQuery := `SELECT COUNT(*) FROM kernel_child_supervision WHERE root_frame_id=?`
	countID := rootFrameID
	if childRecordCount != 0 {
		countQuery = `SELECT COUNT(*) FROM kernel_child_supervision WHERE parent_frame_id=?`
		countID = frameID
	}
	if err := s.db.QueryRowContext(ctx, countQuery, countID).Scan(&spawned); err != nil {
		return KernelDelegationStats{}, err
	}
	remaining := cap - spawned
	if remaining < 0 {
		remaining = 0
	}
	return KernelDelegationStats{SpawnedThisTask: spawned, Cap: cap, Remaining: remaining}, nil
}

func (s *Store) CompleteKernelSupervisedChild(ctx context.Context, frameID, ownerUserID, status string, output map[string]any, failure string, targets ...KernelChildCompletionFence) (KernelSupervisedChild, error) {
	if len(targets) > 1 {
		return KernelSupervisedChild{}, ErrDelegatedStopInvalid
	}
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return KernelSupervisedChild{}, err
	}
	status = strings.TrimSpace(status)
	if status != "completed" && status != "failed" && status != "cancelled" && status != "awaiting_user_response" {
		return KernelSupervisedChild{}, fmt.Errorf("unsupported child status %q", status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KernelSupervisedChild{}, err
	}
	defer tx.Rollback()
	child, found, err := scanKernelSupervisedChild(tx.QueryRowContext(ctx, `
		SELECT frame_id, parent_frame_id, root_frame_id, project_id, owner_user_id, tool_use_id,
			agent_name, delegate_name, task, context_summary, model, output_schema, status,
			output_data, error, dispatched, started_at, completed_at
		FROM kernel_child_supervision WHERE frame_id=? AND owner_user_id=?`, frameID, ownerUserID))
	if err != nil || !found {
		if err != nil {
			return KernelSupervisedChild{}, err
		}
		return KernelSupervisedChild{}, errors.New("delegated child is unavailable")
	}
	var generation int64
	if len(targets) == 1 {
		if err := validateKernelChildCompletionTargetTx(ctx, tx, child, status, targets[0]); err != nil {
			return KernelSupervisedChild{}, err
		}
	}
	_ = tx.QueryRowContext(ctx, `SELECT consumed_generation FROM kernel_child_message_clock WHERE frame_id=?`, frameID).Scan(&generation)
	if isKernelChildTerminalStatus(child.Status) {
		completedAt := s.now().UTC()
		if child.CompletedAt != nil {
			completedAt = child.CompletedAt.UTC()
		}
		if err := createKernelChildCompletionNotificationTx(ctx, tx, child, generation, kernelChildPublicStatus(child.Status, child.Output), child.Output, child.Error, completedAt); err != nil {
			return KernelSupervisedChild{}, err
		}
		if err := tx.Commit(); err != nil {
			return KernelSupervisedChild{}, err
		}
		return child, nil
	}
	stoppedByParent, _ := output["stopped_by_parent"].(bool)
	if (status == "completed" || status == "failed") && !stoppedByParent {
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kernel_child_messages WHERE frame_id=? AND consumed_at IS NULL`, frameID).Scan(&pending); err != nil {
			return KernelSupervisedChild{}, err
		}
		if pending > 0 {
			now := s.now().UTC()
			if _, err := tx.ExecContext(ctx, `UPDATE kernel_child_supervision SET status='processing', completed_at=NULL, error='' WHERE frame_id=?`, frameID); err != nil {
				return KernelSupervisedChild{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE frames SET status='processing', updated_at=? WHERE id=?`, now, frameID); err != nil {
				return KernelSupervisedChild{}, err
			}
			if err := tx.Commit(); err != nil {
				return KernelSupervisedChild{}, err
			}
			child.Status, child.CompletedAt, child.Error = "processing", nil, ""
			return child, nil
		}
	}
	rawOutput, _ := json.Marshal(output)
	now := s.now().UTC()
	var completedAt any
	if isKernelChildTerminalStatus(status) {
		completedAt = now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE kernel_child_supervision SET status=?, output_data=?, error=?, completed_at=? WHERE frame_id=?`,
		status, string(rawOutput), failure, completedAt, frameID); err != nil {
		return KernelSupervisedChild{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE frames SET status=?, updated_at=? WHERE id=?`, status, now, frameID); err != nil {
		return KernelSupervisedChild{}, err
	}
	eventType := "assistant_message"
	payload := map[string]any{"role": "assistant", "status": status, "output_data": output}
	if failure != "" {
		eventType = "frame_failed"
		payload["error"] = failure
	}
	if _, err := appendKernelSupervisionFrameEvent(ctx, tx, FrameEventInput{
		ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("kernel-child-final:%s:%d:%s", frameID, generation, status))).String(), FrameID: frameID,
		Type: eventType, Payload: payload,
	}, now); err != nil {
		return KernelSupervisedChild{}, err
	}
	if isKernelChildTerminalStatus(status) {
		if err := createKernelChildCompletionNotificationTx(ctx, tx, child, generation, kernelChildPublicStatus(status, output), output, failure, now); err != nil {
			return KernelSupervisedChild{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return KernelSupervisedChild{}, err
	}
	child.Status, child.Output, child.Error = status, output, failure
	if isKernelChildTerminalStatus(status) {
		child.CompletedAt = &now
	}
	return child, nil
}

func kernelChildPublicStatus(status string, output map[string]any) string {
	if status == "completed" {
		if stopped, _ := output["stopped_by_parent"].(bool); stopped {
			return "cancelled"
		}
	}
	return status
}

type KernelStopTreeResult struct {
	Target             KernelSupervisedChild
	StoppedDescendants []KernelSupervisedChild
	Applied            bool
}

// Retained internal compatibility entry; all selection/terminal mutation is
// performed by the same frozen host receipt and canonical cancellation core.
func (s *Store) StopKernelSupervisedChildTree(ctx context.Context, parentFrameID, frameID, ownerUserID, reason string) (KernelStopTreeResult, error) {
	access, found, err := s.GetKernelFrameAccessContext(ctx, parentFrameID)
	if err != nil {
		return KernelStopTreeResult{}, err
	}
	if !found || access.UserID != ownerUserID {
		return KernelStopTreeResult{}, ErrDelegatedStopUnavailable
	}
	scope := DelegatedStopScope{ParentFrameID: parentFrameID, ParentFrameIncarnationID: access.Frame.IncarnationID,
		RootFrameID: access.Frame.RootFrameID, RootFrameIncarnationID: access.RootFrameIncarnationID}
	receipt, err := s.AdmitKernelChildStop(ctx, ownerUserID, scope, frameID, reason, uuid.NewString())
	if err != nil {
		return KernelStopTreeResult{}, err
	}
	var result KernelStopTreeResult
	for _, outcome := range receipt.Targets {
		var child KernelSupervisedChild
		var present bool
		if outcome.Target.FrameID == frameID {
			child, present, err = s.GetKernelSupervisedChild(ctx, parentFrameID, frameID, ownerUserID)
			result.Target, result.Applied = child, outcome.Outcome == "cancel_requested"
		} else {
			frame, exists, loadErr := s.GetFrame(outcome.Target.FrameID)
			if loadErr != nil {
				return KernelStopTreeResult{}, loadErr
			}
			if !exists {
				return KernelStopTreeResult{}, ErrDelegatedStopUnavailable
			}
			child, present, err = s.GetKernelSupervisedChild(ctx, frame.ParentFrameID, frame.ID, ownerUserID)
			if outcome.Outcome == "cancel_requested" {
				result.StoppedDescendants = append(result.StoppedDescendants, child)
			}
		}
		if err != nil {
			return KernelStopTreeResult{}, err
		}
		if !present {
			return KernelStopTreeResult{}, ErrDelegatedStopUnavailable
		}
	}
	return result, nil
}

func (s *Store) projectCancelledKernelChildTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, child KernelSupervisedChild, reason string, terminal transcriptstore.FinishReceipt) (KernelSupervisedChild, bool, error) {
	output := make(map[string]any, len(child.Output)+2)
	for key, value := range child.Output {
		output[key] = value
	}
	output["stopped_by_parent"] = true
	output["reason"] = reason
	rawOutput, err := json.Marshal(output)
	if err != nil {
		return KernelSupervisedChild{}, false, errors.New("encode stopped child output")
	}
	now := s.now().UTC()
	updated, err := tx.ExecContext(ctx, `UPDATE kernel_child_supervision
		SET status='completed',output_data=?,error='',completed_at=?
		WHERE frame_id=? AND owner_user_id=?`,
		string(rawOutput), now, child.FrameID, child.OwnerUserID)
	if err != nil {
		return KernelSupervisedChild{}, false, err
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return KernelSupervisedChild{}, false, err
	}
	if changed == 0 {
		current, found, loadErr := scanKernelSupervisedChild(tx.QueryRowContext(ctx, `
			SELECT frame_id,parent_frame_id,root_frame_id,project_id,owner_user_id,tool_use_id,
				agent_name,delegate_name,task,context_summary,model,output_schema,status,
				output_data,error,dispatched,started_at,completed_at
			FROM kernel_child_supervision WHERE frame_id=? AND owner_user_id=?`, child.FrameID, child.OwnerUserID))
		if loadErr != nil || !found {
			if loadErr != nil {
				return KernelSupervisedChild{}, false, loadErr
			}
			return KernelSupervisedChild{}, false, errors.New("delegated child is unavailable")
		}
		return current, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE frames SET status='cancelled',updated_at=? WHERE id=?`, now, child.FrameID); err != nil {
		return KernelSupervisedChild{}, false, err
	}
	var generation int64
	_ = tx.QueryRowContext(ctx, `SELECT consumed_generation FROM kernel_child_message_clock WHERE frame_id=?`, child.FrameID).Scan(&generation)
	if _, err := appendKernelSupervisionFrameEvent(ctx, tx, FrameEventInput{
		ID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("kernel-child-cancelled:%s:%s:%d", child.FrameID, terminal.StreamUID, terminal.EventID))).String(),
		FrameID: child.FrameID, Type: "assistant_message",
		Payload: map[string]any{"role": "assistant", "status": "cancelled", "output_data": output},
	}, now); err != nil {
		return KernelSupervisedChild{}, false, err
	}
	if err := createKernelChildCompletionNotificationTx(ctx, tx, child, generation, "cancelled", output, "", now, terminal); err != nil {
		return KernelSupervisedChild{}, false, err
	}
	// Retain the SDK's historical completed+stopped marker representation.
	// Canonical runner and Frame status above remain genuinely cancelled.
	child.Status, child.Output, child.Error, child.CompletedAt = "completed", output, "", &now
	return child, true, nil
}

func createKernelChildCompletionNotificationTx(
	ctx context.Context,
	tx workspaceTransaction,
	child KernelSupervisedChild,
	generation int64,
	status string,
	output map[string]any,
	failure string,
	completedAt time.Time,
	terminalReceipts ...transcriptstore.FinishReceipt,
) error {
	identity := fmt.Sprintf("%s:%d", child.FrameID, generation)
	if len(terminalReceipts) == 1 {
		receipt := terminalReceipts[0]
		if receipt.StreamUID == "" || receipt.Attempt <= 0 || receipt.EventID <= 0 || receipt.Status != "cancelled" {
			return ErrDelegatedStopInvalid
		}
		identity = fmt.Sprintf("%s:terminal:%s:%d", child.FrameID, receipt.StreamUID, receipt.EventID)
	}
	collectedID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("kernel-child-landing-collected:%s:%s:%d", child.ParentFrameID, child.FrameID, generation))).String()
	if len(terminalReceipts) == 1 {
		collectedID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("kernel-child-landing-collected:"+child.ParentFrameID+":"+identity)).String()
	}
	if _, _, found, err := frameEventByID(ctx, tx, collectedID); err != nil {
		return err
	} else if found {
		return nil
	}
	_, _, err := createNotificationTx(ctx, tx, CreateNotificationInput{
		ID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("kernel-child-notification:"+identity)).String(),
		SenderFrameID: child.FrameID, RecipientFrameID: child.ParentFrameID,
		RootFrameID: child.RootFrameID, OwnerUserID: child.OwnerUserID,
		NotificationType: "child_landed",
		Payload: map[string]any{
			"status": status, "_completion_bullets": kernelCompletionBullets(output, failure),
			"wall_s": completedAt.Sub(child.StartedAt).Seconds(), "child_frame_id": child.FrameID,
			"frame_id": child.FrameID, "agent_name": child.AgentName, "generation": generation,
		},
	}, completedAt)
	return err
}

func (s *Store) FlushUndeliveredKernelChildLandings(ctx context.Context, parentFrameID, rootFrameID, ownerUserID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("workspace store is closed")
	}
	if err := s.ensureKernelSupervisionSchema(ctx); err != nil {
		return 0, err
	}
	flushed := 0
	err := transcriptstore.NewRepository(s.db).RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		if err := validateNotificationAuthorityTx(ctx, tx, parentFrameID, parentFrameID, rootFrameID, ownerUserID, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT child.frame_id FROM kernel_child_supervision child
			LEFT JOIN kernel_child_message_clock clock ON clock.frame_id=child.frame_id
			WHERE child.parent_frame_id=? AND child.root_frame_id=? AND child.owner_user_id=?
				AND child.status IN ('completed','failed','cancelled')
				AND NOT EXISTS (SELECT 1 FROM notifications notification
					WHERE notification.sender_frame_id=child.frame_id
						AND notification.recipient_frame_id=child.parent_frame_id
						AND notification.notification_type='child_landed')
				AND NOT EXISTS (SELECT 1 FROM frame_events collected
					WHERE collected.frame_id=child.parent_frame_id
						AND collected.event_type='kernel_child_landing_collected'
						AND json_extract(collected.payload,'$.child_frame_id')=child.frame_id
						AND json_extract(collected.payload,'$.generation')=COALESCE(clock.consumed_generation,0))
			ORDER BY child.completed_at,child.frame_id`, strings.TrimSpace(parentFrameID), strings.TrimSpace(rootFrameID), strings.TrimSpace(ownerUserID))
		if err != nil {
			return err
		}
		frameIDs := []string{}
		for rows.Next() {
			var frameID string
			if err := rows.Scan(&frameID); err != nil {
				_ = rows.Close()
				return err
			}
			frameIDs = append(frameIDs, frameID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, frameID := range frameIDs {
			child, found, err := scanKernelSupervisedChild(tx.QueryRowContext(ctx, `
				SELECT frame_id,parent_frame_id,root_frame_id,project_id,owner_user_id,tool_use_id,
					agent_name,delegate_name,task,context_summary,model,output_schema,status,
					output_data,error,dispatched,started_at,completed_at
				FROM kernel_child_supervision WHERE frame_id=? AND parent_frame_id=? AND owner_user_id=?`,
				frameID, strings.TrimSpace(parentFrameID), strings.TrimSpace(ownerUserID)))
			if err != nil || !found {
				if err != nil {
					return err
				}
				return errors.New("undelivered kernel child landing is unavailable")
			}
			var generation int64
			_ = tx.QueryRowContext(ctx, `SELECT consumed_generation FROM kernel_child_message_clock WHERE frame_id=?`, frameID).Scan(&generation)
			completedAt := s.now().UTC()
			if child.CompletedAt != nil {
				completedAt = child.CompletedAt.UTC()
			}
			if err := createKernelChildCompletionNotificationTx(ctx, tx, child, generation, child.Status, child.Output, child.Error, completedAt); err != nil {
				return err
			}
			flushed++
		}
		return nil
	})
	return flushed, err
}

func (s *Store) SendKernelSupervisionMessage(ctx context.Context, sourceFrameID, targetFrameID, ownerUserID, message, kind string) (KernelSupervisedChild, string, error) {
	queued, err := s.QueueKernelSupervisionMessage(ctx, sourceFrameID, targetFrameID, ownerUserID, message, kind)
	if err != nil {
		return KernelSupervisedChild{}, "", err
	}
	return queued.Child, queued.Relation, nil
}

func (s *Store) ListKernelChildArtifacts(ctx context.Context, frameID, ownerUserID string) ([]map[string]any, int, error) {
	if s == nil || s.db == nil {
		return nil, 0, errors.New("workspace store is closed")
	}
	var owner string
	if err := s.db.QueryRowContext(ctx, `SELECT p.user_id FROM frames f JOIN projects p ON p.id=f.project_id WHERE f.id=?`, frameID).Scan(&owner); err != nil || owner != ownerUserID {
		return nil, 0, errors.New("delegated child is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE subtree(frame_id) AS (
			SELECT ?
			UNION ALL
			SELECT frame.id FROM frames frame JOIN subtree ON frame.parent_frame_id=subtree.frame_id
		)
		SELECT artifact.id,version.id,artifact.name,
			COALESCE(NULLIF(provenance.content_type,''),artifact.kind),version.size_bytes,version.created_at
		FROM artifact_versions version
		JOIN artifacts artifact ON artifact.id=version.artifact_id
		JOIN artifact_version_provenance provenance ON provenance.version_id=version.id
		JOIN subtree ON subtree.frame_id=provenance.frame_id
		ORDER BY version.created_at ASC`, frameID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	type artifactRow struct {
		artifactID, versionID, filename, contentType string
		sizeBytes                                    int64
		createdAt                                    time.Time
	}
	all := []artifactRow{}
	for rows.Next() {
		var item artifactRow
		if err := rows.Scan(&item.artifactID, &item.versionID, &item.filename, &item.contentType, &item.sizeBytes, &item.createdAt); err != nil {
			return nil, 0, err
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	keep := make([]bool, len(all))
	perArtifact := map[string]int{}
	for index := len(all) - 1; index >= 0; index-- {
		if perArtifact[all[index].artifactID] >= 2 {
			continue
		}
		perArtifact[all[index].artifactID]++
		keep[index] = true
	}
	selected := make([]artifactRow, 0, len(all))
	for index, item := range all {
		if keep[index] {
			selected = append(selected, item)
		}
	}
	if len(selected) > 50 {
		selected = selected[len(selected)-50:]
	}
	items := make([]map[string]any, 0, len(selected))
	for _, item := range selected {
		items = append(items, map[string]any{
			"artifact_id": item.artifactID, "version_id": item.versionID,
			"filename": item.filename, "content_type": item.contentType, "size_bytes": item.sizeBytes,
		})
	}
	return items, len(all) - len(items), nil
}

type kernelSupervisionScanner interface{ Scan(...any) error }

func scanKernelSupervisedChild(scanner kernelSupervisionScanner) (KernelSupervisedChild, bool, error) {
	var child KernelSupervisedChild
	var rawSchema, rawOutput string
	var dispatched int
	var completed sql.NullTime
	err := scanner.Scan(&child.FrameID, &child.ParentFrameID, &child.RootFrameID, &child.ProjectID,
		&child.OwnerUserID, &child.ToolUseID, &child.AgentName, &child.Name, &child.Task,
		&child.ContextSummary, &child.Model, &rawSchema, &child.Status, &rawOutput, &child.Error,
		&dispatched, &child.StartedAt, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return KernelSupervisedChild{}, false, nil
	}
	if err != nil {
		return KernelSupervisedChild{}, false, fmt.Errorf("scan delegated child: %w", err)
	}
	child.Dispatched = dispatched != 0
	if completed.Valid {
		child.CompletedAt = &completed.Time
	}
	if strings.TrimSpace(rawSchema) != "" && rawSchema != "null" {
		if err := json.Unmarshal([]byte(rawSchema), &child.OutputSchema); err != nil {
			return KernelSupervisedChild{}, false, fmt.Errorf("decode child output schema: %w", err)
		}
	}
	if strings.TrimSpace(rawOutput) != "" && rawOutput != "null" {
		if err := json.Unmarshal([]byte(rawOutput), &child.Output); err != nil {
			return KernelSupervisedChild{}, false, fmt.Errorf("decode child output: %w", err)
		}
	}
	return child, true, nil
}

func isKernelChildTerminalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "orphaned"
}

func kernelCompletionBullets(output map[string]any, failure string) []any {
	if strings.TrimSpace(failure) != "" {
		return []any{"Child run failed", failure}
	}
	if response, _ := output["response"].(string); strings.TrimSpace(response) != "" {
		return []any{"Child run completed", truncateKernelSupervisionText(response, 240)}
	}
	if output["structured_output"] != nil {
		return []any{"Child run completed", "Structured output was submitted"}
	}
	return []any{"Child run completed", "No prose response was recorded"}
}

func truncateKernelSupervisionText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}
