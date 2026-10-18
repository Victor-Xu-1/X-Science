package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

// The frame, supervision relationship, origin and canonical first input commit
// together. A queued child is never visible without an input that Stop can fence.
func (s *Store) createKernelSupervisedChildrenTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, input CreateKernelDelegatesInput) ([]KernelSupervisedChild, error) {
	if children, replayed, err := s.validateDelegationAdmissionTx(ctx, tx, input); err != nil || replayed {
		return children, err
	}
	var projectID, rootFrameID, parentAgent, owner string
	if err := tx.QueryRowContext(ctx, `
		SELECT f.project_id, f.root_frame_id, f.agent_name, p.user_id
		FROM frames f JOIN projects p ON p.id = f.project_id WHERE f.id = ?`, input.ParentFrameID,
	).Scan(&projectID, &rootFrameID, &parentAgent, &owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("kernel delegation parent is unavailable")
		}
		return nil, fmt.Errorf("load kernel delegation parent: %w", err)
	}
	if owner != input.OwnerUserID {
		return nil, errors.New("kernel delegation parent is unavailable")
	}
	depth := 1
	ancestorID := input.ParentFrameID
	for {
		var nextParent string
		err := tx.QueryRowContext(ctx, `SELECT parent_frame_id FROM kernel_child_supervision WHERE frame_id = ?`, ancestorID).Scan(&nextParent)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("resolve delegation depth: %w", err)
		}
		depth++
		if depth > input.MaxDepth {
			return nil, fmt.Errorf("delegation max depth %d exceeded", input.MaxDepth)
		}
		ancestorID = nextParent
	}
	var spawned int
	countQuery := `SELECT COUNT(*) FROM kernel_child_supervision WHERE root_frame_id = ?`
	countID := rootFrameID
	if depth > 1 {
		countQuery = `SELECT COUNT(*) FROM kernel_child_supervision WHERE parent_frame_id = ?`
		countID = input.ParentFrameID
	}
	if err := tx.QueryRowContext(ctx, countQuery, countID).Scan(&spawned); err != nil {
		return nil, fmt.Errorf("count kernel delegations: %w", err)
	}
	if spawned+len(input.Requests) > input.SpawnCap {
		return nil, fmt.Errorf("delegation spawn cap exceeded: spawned=%d requested=%d cap=%d", spawned, len(input.Requests), input.SpawnCap)
	}

	now := s.now().UTC()
	children := make([]KernelSupervisedChild, 0, len(input.Requests))
	toolMapping := map[string]any{}
	var rawParentContext sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT context_data FROM frame_runtime_metadata WHERE frame_id = ?`, input.ParentFrameID).Scan(&rawParentContext)
	parentContext := map[string]any{}
	if rawParentContext.Valid && strings.TrimSpace(rawParentContext.String) != "" {
		_ = json.Unmarshal([]byte(rawParentContext.String), &parentContext)
	}
	if existing, ok := parentContext["_tool_id_to_frame_id"].(map[string]any); ok {
		for key, value := range existing {
			toolMapping[key] = value
		}
	}

	for index, request := range input.Requests {
		request.Task = strings.TrimSpace(request.Task)
		if request.Task == "" {
			return nil, fmt.Errorf("delegate request %d task is required", index)
		}
		profile := strings.TrimSpace(request.Profile)
		if profile == "" {
			profile = parentAgent
		}
		if depth > 1 && !strings.EqualFold(profile, parentAgent) {
			return nil, fmt.Errorf("delegate request %d nested profile must match its parent profile", index)
		}
		frameID := uuid.NewString()
		incarnationID := uuid.NewString()
		var rootSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(root_sequence), 0) + 1 FROM frames WHERE root_frame_id = ?`, rootFrameID).Scan(&rootSequence); err != nil {
			return nil, fmt.Errorf("allocate child root sequence: %w", err)
		}
		name := strings.TrimSpace(request.Name)
		if name == "" {
			name = profile
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO frames (id, incarnation_id, project_id, parent_frame_id, root_frame_id, root_sequence, agent_name, status, conversation_type, name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'processing', 'delegate', ?, ?, ?)`,
			frameID, incarnationID, projectID, input.ParentFrameID, rootFrameID, rootSequence, profile, name, now, now); err != nil {
			return nil, fmt.Errorf("insert delegated child frame: %w", err)
		}
		contextData := map[string]any{
			"task": request.Task, "context_summary": request.ContextSummary,
			"profile": profile, "model": request.Model, "output_schema": request.OutputSchema,
			"_latest_tool_block": map[string]any{"tool_use_id": input.ToolUseID, "status": "processing"},
		}
		rawInput, _ := json.Marshal(map[string]any{"request": request.Task})
		rawContext, _ := json.Marshal(contextData)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO frame_runtime_metadata (frame_id, delegate_name, input_data, context_data, model, task_summary)
			VALUES (?, ?, ?, ?, ?, ?)`, frameID, nullableString(request.Name), string(rawInput), string(rawContext), nullableString(request.Model), request.Task); err != nil {
			return nil, fmt.Errorf("insert delegated child metadata: %w", err)
		}
		rawSchema, _ := json.Marshal(request.OutputSchema)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kernel_child_supervision
			(frame_id, parent_frame_id, root_frame_id, project_id, owner_user_id, tool_use_id, agent_name, delegate_name, task, context_summary, model, output_schema, status, started_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'processing', ?)`,
			frameID, input.ParentFrameID, rootFrameID, projectID, owner, input.ToolUseID, profile,
			request.Name, request.Task, request.ContextSummary, request.Model, string(rawSchema), now); err != nil {
			return nil, fmt.Errorf("insert child supervision record: %w", err)
		}
		toolMapping[input.ToolUseID+fmt.Sprintf(":%d", index)] = frameID
		if len(input.Requests) == 1 {
			toolMapping[input.ToolUseID] = frameID
		}
		children = append(children, KernelSupervisedChild{
			FrameID: frameID, ParentFrameID: input.ParentFrameID, RootFrameID: rootFrameID,
			ProjectID: projectID, OwnerUserID: owner, ToolUseID: input.ToolUseID,
			AgentName: profile, Name: request.Name, Task: request.Task,
			ContextSummary: request.ContextSummary, Model: request.Model,
			OutputSchema: request.OutputSchema, Status: "processing", StartedAt: now,
		})
		if err := s.createDelegatedInitialInputTx(ctx, tx, children[len(children)-1], incarnationID, input.Admission); err != nil {
			return nil, err
		}
	}
	parentContext["_tool_id_to_frame_id"] = toolMapping
	rawUpdatedContext, _ := json.Marshal(parentContext)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO frame_runtime_metadata (frame_id, context_data) VALUES (?, ?)
		ON CONFLICT(frame_id) DO UPDATE SET context_data = excluded.context_data`, input.ParentFrameID, string(rawUpdatedContext)); err != nil {
		return nil, fmt.Errorf("persist parent child mapping: %w", err)
	}
	childIDs := make([]any, len(children))
	for index := range children {
		childIDs[index] = children[index].FrameID
	}
	if _, err := appendKernelSupervisionFrameEvent(ctx, tx, FrameEventInput{
		ID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte("kernel-delegate-tool-use:"+input.ParentFrameID+":"+input.ToolUseID)).String(),
		FrameID: input.ParentFrameID, Type: "tool_use", Payload: map[string]any{
			"tool_use_id": input.ToolUseID, "tool_name": "delegate", "child_frame_ids": childIDs,
		},
	}, now); err != nil {
		return nil, err
	}
	if err := s.completeDelegationAdmissionTx(ctx, tx, input, children); err != nil {
		return nil, err
	}
	return children, nil
}
