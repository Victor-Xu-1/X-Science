package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type compatibilityInputProjection struct {
	resolved       map[string]bool
	messages       []compatibilityStoredInputMessage
	remainingIDs   []string
	remainingCount int
}

// Shared projection only. Callers retain their own exact admission/typed-result
// authority and decide whether the existing dispatcher needs a new receipt.
func projectCompatibilityInputResolutionsTx(ctx context.Context, tx workspaceTransaction, frameID string, contextData map[string]any, resolutions []CompatibilityInputResolution) (compatibilityInputProjection, error) {
	resolved := compatibilityResolvedInputSet(contextData)
	pending := compatibilityPendingInputRequests(contextData)
	pendingByID := make(map[string]map[string]any, len(pending))
	for _, item := range pending {
		if id := compatibilityPendingInputID(item); id != "" {
			pendingByID[id] = item
		}
	}
	for _, resolution := range resolutions {
		if resolved[resolution.ToolID] {
			continue
		}
		if _, found := pendingByID[resolution.ToolID]; !found {
			return compatibilityInputProjection{}, fmt.Errorf("No pending request with tool_id %s.", resolution.ToolID)
		}
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, payload FROM frame_events
		WHERE frame_id = ? AND `+compatibilityMessagePredicate+` ORDER BY sequence`, frameID)
	if err != nil {
		return compatibilityInputProjection{}, fmt.Errorf("list compatibility input messages: %w", err)
	}
	messages := make([]compatibilityStoredInputMessage, 0)
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return compatibilityInputProjection{}, err
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			_ = rows.Close()
			return compatibilityInputProjection{}, err
		}
		messages = append(messages, compatibilityStoredInputMessage{id: id, payload: payload})
	}
	if err := rows.Close(); err != nil {
		return compatibilityInputProjection{}, err
	}
	for _, resolution := range resolutions {
		if resolved[resolution.ToolID] {
			continue
		}
		for index := range messages {
			if rewriteCompatibilityToolResult(messages[index].payload, resolution) {
				messages[index].changed = true
				break
			}
		}
		resolved[resolution.ToolID] = true
	}
	for _, message := range messages {
		if !message.changed {
			continue
		}
		raw, err := json.Marshal(message.payload)
		if err != nil {
			return compatibilityInputProjection{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE frame_events SET payload = ? WHERE id = ?`, string(raw), message.id); err != nil {
			return compatibilityInputProjection{}, fmt.Errorf("rewrite compatibility tool result: %w", err)
		}
	}
	remaining := make([]map[string]any, 0, len(pending))
	remainingIDs := make([]string, 0, len(pending))
	for _, item := range pending {
		id := compatibilityPendingInputID(item)
		if id != "" && resolved[id] {
			continue
		}
		remaining = append(remaining, item)
		if id != "" {
			remainingIDs = append(remainingIDs, id)
		}
	}
	contextData["_pending_input_requests"] = compatibilityMapsToAny(remaining)
	contextData["_resolved_input_tool_ids"] = compatibilitySortedKeys(resolved)
	if len(remaining) == 0 {
		delete(contextData, "_ask_user_payload")
		delete(contextData, "_pending_access_request")
		delete(contextData, inputTranscriptStreamUIDKey)
	}
	return compatibilityInputProjection{resolved: resolved, messages: messages, remainingIDs: remainingIDs, remainingCount: len(remaining)}, nil
}

func persistCompatibilityInputContextTx(ctx context.Context, tx workspaceTransaction, frameID string, contextData map[string]any) error {
	rawUpdated, err := json.Marshal(contextData)
	if err != nil {
		return err
	}
	metadataUpdate, err := tx.ExecContext(ctx, `
		UPDATE frame_runtime_metadata SET context_data = ? WHERE frame_id = ?`, string(rawUpdated), frameID)
	if err != nil {
		return fmt.Errorf("persist compatibility input context: %w", err)
	}
	if changed, err := metadataUpdate.RowsAffected(); err != nil {
		return err
	} else if changed != 1 {
		return errors.New("frame runtime metadata is missing")
	}
	return nil
}
