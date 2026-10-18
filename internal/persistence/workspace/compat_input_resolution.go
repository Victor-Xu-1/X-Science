package workspace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

type CompatibilityInputResolution struct {
	ToolID            string
	Content           string
	ModelContinuation string
	IsError           bool
	AskUserResult     *transcriptstore.AskUserResultV1
	AskUserOrigin     *transcriptstore.AskUserOriginV1
}

type CompatibilityInputResolutionResult struct {
	Frame           CompatibilityFrame
	Status          string
	RemainingIDs    []string
	AlreadyResolved bool
	InputEvent      *FrameEvent
	Event           FrameEvent
	DispatchEvent   *FrameEvent
}

type compatibilityStoredInputMessage struct {
	id      string
	payload map[string]any
	changed bool
}

type compatibilityInputResponseAppender func(
	context.Context, workspaceTransaction, map[string]any,
) (*FrameEvent, error)

func (s *Store) ResolveCompatibilityPendingInputs(frameID string, resolutions []CompatibilityInputResolution) (CompatibilityInputResolutionResult, error) {
	if s == nil || s.db == nil {
		return CompatibilityInputResolutionResult{}, errors.New("workspace store is closed")
	}
	frameID = strings.TrimSpace(frameID)
	if frameID == "" || len(resolutions) == 0 {
		return CompatibilityInputResolutionResult{}, errors.New("frame id and input resolutions are required")
	}
	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompatibilityInputResolutionResult{}, fmt.Errorf("begin compatibility input resolution: %w", err)
	}
	defer tx.Rollback()
	result, err := s.resolveCompatibilityPendingInputsInTransaction(ctx, tx, frameID, resolutions, nil, nil)
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	hydrated, err := s.hydrateCompatibilityInputResolution(frameID, result)
	if err == nil {
		s.signalKernelRetentionWake()
	}
	return hydrated, err
}

func (s *Store) ResolveCompatibilityPendingInputsWithTranscript(
	ctx context.Context,
	frameID string,
	resolutions []CompatibilityInputResolution,
) (CompatibilityInputResolutionResult, error) {
	return s.ResolveCompatibilityPendingInputsWithTranscriptConfig(ctx, frameID, resolutions, nil)
}

func (s *Store) ResolveCompatibilityPendingInputsWithTranscriptConfig(
	ctx context.Context,
	frameID string,
	resolutions []CompatibilityInputResolution,
	runtimeConfig map[string]any,
) (CompatibilityInputResolutionResult, error) {
	if s == nil || s.db == nil {
		return CompatibilityInputResolutionResult{}, errors.New("workspace store is closed")
	}
	frameID = strings.TrimSpace(frameID)
	if frameID == "" || len(resolutions) == 0 {
		return CompatibilityInputResolutionResult{}, errors.New("frame id and input resolutions are required")
	}
	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	repository := transcriptstore.NewRepository(s.db)
	var result CompatibilityInputResolutionResult
	err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		streamUID, allAlreadyResolved, retryResult, err := s.appendTranscriptAskUserResults(
			ctx, tx, frameID, resolutions,
		)
		if err != nil {
			return err
		}
		if allAlreadyResolved {
			result = retryResult
			return nil
		}
		result, err = s.resolveCompatibilityPendingInputsInTransaction(ctx, tx, frameID, resolutions, runtimeConfig,
			func(ctx context.Context, workspaceTx workspaceTransaction, payload map[string]any) (*FrameEvent, error) {
				ownerID, err := frameOwnerInTransaction(ctx, workspaceTx, frameID)
				if err != nil {
					return nil, err
				}
				// This aggregate is model-only continuation. It retains a durable
				// delivery receipt for ordering/recovery, while the Web projector
				// acknowledges it without creating a second user bubble.
				authority, found, err := tx.GetFrameAuthorityBySession(ctx, ownerID, frameID)
				if err != nil {
					return nil, fmt.Errorf("load input response authority: %w", err)
				}
				if streamUID == "" && found {
					streamUID = authority.ActiveStreamUID
				}
				if !found || authority.ActiveStreamUID != streamUID {
					return nil, fmt.Errorf("input response stream authority: %w", transcriptstore.ErrEventConflict)
				}
				if authority.TranscriptPayloadActive() {
					encoded, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					_, _, err = tx.AppendFrameInputResponse(ctx, transcriptstore.AppendFrameInputResponseInput{
						StreamUID: streamUID, OwnerID: ownerID, FrameID: frameID,
						ClientMessageID: compatibilityInputResponseClientID(streamUID, encoded),
						PayloadJSON:     encoded, Destinations: []string{"ws"},
					})
					if err != nil {
						return nil, fmt.Errorf("append payload input response: %w", err)
					}
					return nil, nil
				}
				frameEvent, err := appendFrameLifecycleEvent(ctx, workspaceTx, frameID, "user_input_response", payload, s.now().UTC())
				if err != nil {
					return nil, err
				}
				_, _, err = tx.AppendFrameInputResponse(ctx, transcriptstore.AppendFrameInputResponseInput{
					StreamUID: streamUID, OwnerID: ownerID, FrameID: frameID,
					ClientMessageID: "input-response:" + frameEvent.ID, FrameEventID: frameEvent.ID,
					Destinations: []string{"ws"},
				})
				return &frameEvent, err
			})
		return err
	})
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	hydrated, err := s.hydrateCompatibilityInputResolution(frameID, result)
	if err == nil {
		s.signalKernelRetentionWake()
	}
	return hydrated, err
}

func (s *Store) appendTranscriptAskUserResults(
	ctx context.Context,
	tx *transcriptstore.ImmediateTransaction,
	frameID string,
	resolutions []CompatibilityInputResolution,
) (string, bool, CompatibilityInputResolutionResult, error) {
	var ownerID, status, rawContext string
	if err := tx.QueryRowContext(ctx, `
		SELECT project.user_id,frame.status,COALESCE(metadata.context_data,'{}')
		FROM frames frame JOIN projects project ON project.id=frame.project_id
		LEFT JOIN frame_runtime_metadata metadata ON metadata.frame_id=frame.id
		WHERE frame.id=?`, frameID,
	).Scan(&ownerID, &status, &rawContext); err != nil {
		return "", false, CompatibilityInputResolutionResult{}, err
	}
	contextData := map[string]any{}
	if json.Unmarshal([]byte(rawContext), &contextData) != nil {
		return "", false, CompatibilityInputResolutionResult{}, errors.New("ask-user frame context is invalid")
	}
	streamUID := strings.TrimSpace(compatibilityStringValue(contextData[inputTranscriptStreamUIDKey]))
	origins, _ := contextData[askUserTranscriptOriginsKey].(map[string]any)
	resolved := compatibilityResolvedInputSet(contextData)
	allAlreadyResolved := len(resolutions) > 0
	for index := range resolutions {
		resolution := &resolutions[index]
		resolution.ToolID = strings.TrimSpace(resolution.ToolID)
		if resolution.AskUserResult == nil {
			if resolution.AskUserOrigin != nil {
				return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
			}
			allAlreadyResolved = false
			continue
		}
		if resolution.AskUserOrigin == nil {
			return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
		}
		encodedResult, err := transcriptstore.EncodeAskUserResultV1(*resolution.AskUserResult)
		if err != nil || resolution.Content != string(encodedResult) ||
			strings.TrimSpace(resolution.ModelContinuation) == "" || resolution.IsError {
			return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
		}
		rawOrigin, found := origins[resolution.ToolID]
		if !found {
			return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
		}
		encoded, err := json.Marshal(rawOrigin)
		if err != nil {
			return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
		}
		origin, err := transcriptstore.DecodeAskUserOriginV1(encoded)
		if err != nil || origin.ToolUseID != resolution.ToolID ||
			origin != *resolution.AskUserOrigin || (streamUID != "" && streamUID != origin.StreamUID) {
			return "", false, CompatibilityInputResolutionResult{}, transcriptstore.ErrEventConflict
		}
		if streamUID == "" {
			streamUID = origin.StreamUID
		}
		_, created, err := tx.AppendFrameAskUserResult(ctx, transcriptstore.AppendFrameAskUserResultInput{
			OwnerID: ownerID, FrameID: frameID, Origin: origin, Result: *resolution.AskUserResult,
			ModelContinuation: resolution.ModelContinuation,
		})
		if err != nil {
			return "", false, CompatibilityInputResolutionResult{}, err
		}
		if created || !resolved[resolution.ToolID] {
			allAlreadyResolved = false
		}
	}
	if !allAlreadyResolved {
		return streamUID, false, CompatibilityInputResolutionResult{}, nil
	}
	pending := compatibilityPendingInputRequests(contextData)
	remainingIDs := make([]string, 0, len(pending))
	for _, item := range pending {
		if id := compatibilityPendingInputID(item); id != "" && !resolved[id] {
			remainingIDs = append(remainingIDs, id)
		}
	}
	sort.Strings(remainingIDs)
	retryStatus := "already_resolved"
	if status == "awaiting_user_response" && len(remainingIDs) > 0 {
		retryStatus = "partial"
	}
	return streamUID, true, CompatibilityInputResolutionResult{
		Status: retryStatus, RemainingIDs: remainingIDs, AlreadyResolved: true,
	}, nil
}

func (s *Store) resolveCompatibilityPendingInputsInTransaction(
	ctx context.Context,
	tx workspaceTransaction,
	frameID string,
	resolutions []CompatibilityInputResolution,
	runtimeConfig map[string]any,
	appendResponse compatibilityInputResponseAppender,
) (CompatibilityInputResolutionResult, error) {

	var status, rawContext, rootFrameID, agentName string
	if err := tx.QueryRowContext(ctx, `
		SELECT f.status, COALESCE(m.context_data, '{}'), f.root_frame_id, f.agent_name
		FROM frames f LEFT JOIN frame_runtime_metadata m ON m.frame_id = f.id
		WHERE f.id = ?`, frameID).Scan(&status, &rawContext, &rootFrameID, &agentName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CompatibilityInputResolutionResult{}, fmt.Errorf("frame %s not found", frameID)
		}
		return CompatibilityInputResolutionResult{}, fmt.Errorf("load compatibility pending inputs: %w", err)
	}
	contextData := map[string]any{}
	if err := json.Unmarshal([]byte(rawContext), &contextData); err != nil {
		return CompatibilityInputResolutionResult{}, fmt.Errorf("decode compatibility pending input context: %w", err)
	}
	resolved := compatibilityResolvedInputSet(contextData)
	if status != "awaiting_user_response" {
		allResolved := true
		for _, resolution := range resolutions {
			if !resolved[resolution.ToolID] {
				allResolved = false
				break
			}
		}
		if allResolved {
			return CompatibilityInputResolutionResult{Status: "already_resolved", AlreadyResolved: true}, nil
		}
		return CompatibilityInputResolutionResult{}, fmt.Errorf("Frame not awaiting user response. Status: %s.", status)
	}
	projection, err := projectCompatibilityInputResolutionsTx(ctx, tx, frameID, contextData, resolutions)
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	remainingIDs := projection.remainingIDs
	var inputEvent *FrameEvent
	if projection.remainingCount == 0 && appendResponse != nil {
		payload, err := compatibilityResolvedInputPayload(projection.resolved, projection.messages)
		if err != nil {
			return CompatibilityInputResolutionResult{}, err
		}
		if len(runtimeConfig) > 0 {
			config := make(map[string]any, len(runtimeConfig))
			for key, value := range runtimeConfig {
				config[key] = value
			}
			payload["runtimeConfig"] = config
		}
		appended, err := appendResponse(ctx, tx, payload)
		if err != nil {
			return CompatibilityInputResolutionResult{}, err
		}
		inputEvent = appended
	}
	if err := persistCompatibilityInputContextTx(ctx, tx, frameID, contextData); err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	nextStatus := "processing"
	responseStatus := "accepted"
	if projection.remainingCount > 0 {
		nextStatus = "awaiting_user_response"
		responseStatus = "partial"
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE frames SET status = ?, updated_at = ?
		WHERE id = ? AND status = 'awaiting_user_response'`, nextStatus, s.now().UTC(), frameID)
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return CompatibilityInputResolutionResult{}, err
		}
		return CompatibilityInputResolutionResult{}, errors.New("Frame state changed while resolving input — refresh and retry.")
	}
	event, err := appendFrameLifecycleEvent(ctx, tx, frameID, "input_requests_resolved", map[string]any{
		"status": responseStatus, "remaining_tool_ids": remainingIDs,
	}, s.now().UTC())
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	var dispatchEvent *FrameEvent
	if responseStatus == "accepted" {
		now := s.now().UTC()
		resumed, err := appendFrameLifecycleEvent(ctx, tx, frameID, "frame_resumed", map[string]any{
			"previousStatus": "awaiting_user_response",
			"rootFrameId":    rootFrameID,
			"agentName":      agentName,
			"reason":         "input_requests_resolved",
			"dispatch": map[string]any{
				"status": frameResumeDispatchRegistered, "attempt": 0,
				"registeredAt": now.Format(time.RFC3339Nano),
			},
		}, now)
		if err != nil {
			return CompatibilityInputResolutionResult{}, err
		}
		dispatchEvent = &resumed
	}
	return CompatibilityInputResolutionResult{
		Status: responseStatus, RemainingIDs: remainingIDs, InputEvent: inputEvent, Event: event, DispatchEvent: dispatchEvent,
	}, nil
}

func (s *Store) hydrateCompatibilityInputResolution(
	frameID string,
	result CompatibilityInputResolutionResult,
) (CompatibilityInputResolutionResult, error) {
	frame, found, err := s.GetCompatibilityFrame(frameID)
	if err != nil {
		return CompatibilityInputResolutionResult{}, err
	}
	if !found {
		return CompatibilityInputResolutionResult{}, fmt.Errorf("frame %s not found", frameID)
	}
	result.Frame = frame
	return result, nil
}

func compatibilityResolvedInputPayload(
	resolved map[string]bool,
	messages []compatibilityStoredInputMessage,
) (map[string]any, error) {
	ids := make([]string, 0, len(resolved))
	for id, isResolved := range resolved {
		if isResolved {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		content, found := compatibilityResolvedToolResult(messages, id)
		if !found {
			return nil, fmt.Errorf("resolved input %s has no durable tool result", id)
		}
		lines = append(lines, id+": "+content)
	}
	if len(lines) == 0 {
		return nil, errors.New("resolved input response is empty")
	}
	text := "Resolved input requests:\n" + strings.Join(lines, "\n")
	return map[string]any{
		"role": "user", "messageOrigin": "input_response", "text": text,
		"content": []any{map[string]any{"type": "text", "text": text}},
	}, nil
}

func compatibilityInputResponseClientID(streamUID string, payload []byte) string {
	digest := sha256.Sum256(append(
		[]byte("x-science.transcript.input-response.v1\x00"+strings.TrimSpace(streamUID)+"\x00"), payload...,
	))
	return fmt.Sprintf("input-response:%x", digest)
}

func compatibilityResolvedToolResult(messages []compatibilityStoredInputMessage, toolID string) (string, bool) {
	for _, message := range messages {
		blocks, _ := message.payload["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if block["type"] != "tool_result" || compatibilityStringValue(block["tool_use_id"]) != toolID {
				continue
			}
			if continuation := strings.TrimSpace(compatibilityStringValue(block["model_continuation"])); continuation != "" {
				return continuation, true
			}
			return compatibilityStringValue(block["content"]), true
		}
	}
	return "", false
}

func compatibilityPendingInputID(item map[string]any) string {
	if id := strings.TrimSpace(compatibilityStringValue(item["tool_id"])); id != "" {
		return id
	}
	return strings.TrimSpace(compatibilityStringValue(item["requestId"]))
}

func compatibilityPendingInputRequests(contextData map[string]any) []map[string]any {
	result := make([]map[string]any, 0)
	if raw, ok := contextData["_pending_input_requests"].([]any); ok {
		for _, value := range raw {
			if item, ok := value.(map[string]any); ok {
				result = append(result, item)
			}
		}
	}
	if len(result) == 0 {
		for _, key := range []string{"_ask_user_payload", "_pending_access_request"} {
			if item, ok := contextData[key].(map[string]any); ok {
				result = append(result, item)
			}
		}
	}
	return result
}

func compatibilityResolvedInputSet(contextData map[string]any) map[string]bool {
	result := map[string]bool{}
	if values, ok := contextData["_resolved_input_tool_ids"].([]any); ok {
		for _, value := range values {
			if id := strings.TrimSpace(compatibilityStringValue(value)); id != "" {
				result[id] = true
			}
		}
	}
	return result
}

func rewriteCompatibilityToolResult(message map[string]any, resolution CompatibilityInputResolution) bool {
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block["type"] != "tool_result" || compatibilityStringValue(block["tool_use_id"]) != resolution.ToolID {
			continue
		}
		block["content"] = resolution.Content
		if continuation := strings.TrimSpace(resolution.ModelContinuation); continuation != "" {
			block["model_continuation"] = continuation
		} else {
			delete(block, "model_continuation")
		}
		if resolution.IsError {
			block["is_error"] = true
		} else {
			delete(block, "is_error")
		}
		return true
	}
	return false
}

func compatibilityMapsToAny(values []map[string]any) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func compatibilitySortedKeys(values map[string]bool) []any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]any, len(keys))
	for index := range keys {
		result[index] = keys[index]
	}
	return result
}
