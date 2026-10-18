package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

const kernelParentDiscussionKey = "kernel_parent_discussion"

type kernelParentDiscussion struct {
	Version        int                             `json:"version"`
	QueueID        string                          `json:"queue_id"`
	Origin         transcriptstore.AskUserOriginV1 `json:"origin"`
	ResultClientID string                          `json:"result_client_id"`
}

func (s *Store) prepareKernelChildMessageInputTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, child KernelSupervisedChild, expected DelegatedStopTarget, message KernelChildQueuedMessage, identity string) (string, map[string]any, error) {
	data := map[string]any{"notification_type": "child_message", "sender_frame_id": message.SenderFrameID, "kind": message.Kind}
	var kind, raw string
	err := tx.QueryRowContext(ctx, `SELECT event_type,payload_json FROM transcript_events WHERE stream_uid=? AND client_message_id=?`, expected.StreamUID, identity).Scan(&kind, &raw)
	if err == nil {
		return replayKernelChildMessageInputTx(ctx, tx, child, expected, message, kind, raw, data)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	var phase, status string
	var attempt int64
	err = tx.QueryRowContext(ctx, `SELECT phase,status,attempt FROM transcript_runner_attempts WHERE stream_uid=? ORDER BY attempt DESC LIMIT 1`, expected.StreamUID).Scan(&phase, &status, &attempt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if status != "running" || phase != "waiting_user" {
		return "task_intent", data, nil
	}
	// A parent agent can discuss its child's one exact question, but cannot
	// impersonate a human answer, choose an implementation, or approve a tool.
	var rawContext string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(context_data,'{}') FROM frame_runtime_metadata WHERE frame_id=?`, child.FrameID).Scan(&rawContext); err != nil {
		return "", nil, err
	}
	contextData := map[string]any{}
	if json.Unmarshal([]byte(rawContext), &contextData) != nil {
		return "", nil, transcriptstore.ErrEventConflict
	}
	pending := compatibilityPendingInputRequests(contextData)
	if len(pending) == 0 && contextData["_pending_access_request"] == nil {
		// A prior message in this same atomic queue batch can have resolved the
		// question. Additional notes remain ordinary inputs, not more answers.
		return "task_intent", data, nil
	}
	if message.SenderFrameID != child.ParentFrameID || len(pending) != 1 || pending[0]["kind"] != "ask" || contextData["_pending_access_request"] != nil {
		return "", nil, errors.New("child pending input requires its exact user resolution")
	}
	id := compatibilityPendingInputID(pending[0])
	origins, _ := contextData[askUserTranscriptOriginsKey].(map[string]any)
	encodedOrigin, err := json.Marshal(origins[id])
	if err != nil {
		return "", nil, err
	}
	origin, err := transcriptstore.DecodeAskUserOriginV1(encodedOrigin)
	if err != nil || origin.ToolUseID != id || origin.FrameID != child.FrameID || origin.StreamUID != expected.StreamUID || origin.Epoch != expected.StreamEpoch || origin.RunnerAttempt != attempt {
		return "", nil, transcriptstore.ErrEventConflict
	}
	resolution, err := kernelChildDiscussionResolution(message, origin)
	if err != nil {
		return "", nil, err
	}
	uid, _, _, err := s.appendTranscriptAskUserResults(ctx, tx, child.FrameID, []CompatibilityInputResolution{resolution})
	if err != nil {
		return "", nil, err
	}
	if uid != expected.StreamUID {
		return "", nil, transcriptstore.ErrEventConflict
	}
	projection, err := projectCompatibilityInputResolutionsTx(ctx, tx, child.FrameID, contextData, []CompatibilityInputResolution{resolution})
	if err != nil {
		return "", nil, err
	}
	if projection.remainingCount != 0 {
		return "", nil, transcriptstore.ErrEventConflict
	}
	if err := persistCompatibilityInputContextTx(ctx, tx, child.FrameID, contextData); err != nil {
		return "", nil, err
	}
	if _, err := appendFrameLifecycleEvent(ctx, tx, child.FrameID, "input_requests_resolved", map[string]any{
		"status": "accepted", "remaining_tool_ids": []string{}, "source": "parent_agent_discussion", "queue_id": message.ID,
	}, s.now().UTC()); err != nil {
		return "", nil, err
	}
	resultID, err := transcriptstore.AskUserResultClientMessageIDV1(origin)
	if err != nil {
		return "", nil, err
	}
	data[kernelParentDiscussionKey] = kernelParentDiscussion{Version: 1, QueueID: message.ID, Origin: origin, ResultClientID: resultID}
	return "input_response", data, nil
}

func kernelChildDiscussionResolution(message KernelChildQueuedMessage, origin transcriptstore.AskUserOriginV1) (CompatibilityInputResolution, error) {
	result, err := transcriptstore.NewAskUserResultV1(transcriptstore.AskUserActionDiscuss, nil, message.Message)
	if err != nil {
		return CompatibilityInputResolution{}, err
	}
	encoded, err := transcriptstore.EncodeAskUserResultV1(result)
	if err != nil {
		return CompatibilityInputResolution{}, err
	}
	return CompatibilityInputResolution{ToolID: origin.ToolUseID, Content: string(encoded), AskUserResult: &result, AskUserOrigin: &origin,
		ModelContinuation: fmt.Sprintf("Parent agent %s sent queued message %s: %s\nThis is parent-agent discussion, not a human answer, option selection, delegation, or permission approval.", message.SenderFrameID, message.ID, message.Message)}, nil
}

// Replay never resolves a newer prompt. Its original queue input and typed
// discussion result must agree before the original immutable payload is reused.
func replayKernelChildMessageInputTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, child KernelSupervisedChild, expected DelegatedStopTarget, message KernelChildQueuedMessage, kind, raw string, data map[string]any) (string, map[string]any, error) {
	var payload struct {
		Text      string         `json:"text"`
		InputData map[string]any `json:"inputData"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || payload.Text != strings.TrimSpace(message.Message) || payload.InputData["sender_frame_id"] != message.SenderFrameID || payload.InputData["kind"] != message.Kind || payload.InputData["notification_type"] != "child_message" {
		return "", nil, transcriptstore.ErrEventConflict
	}
	if proof, ok := payload.InputData[kernelParentDiscussionKey]; ok {
		encoded, err := json.Marshal(proof)
		var discussion kernelParentDiscussion
		if err != nil || json.Unmarshal(encoded, &discussion) != nil || discussion.Version != 1 || discussion.QueueID != message.ID || discussion.Origin.FrameID != child.FrameID || discussion.Origin.StreamUID != expected.StreamUID || discussion.Origin.Epoch != expected.StreamEpoch || kind != "user_input_response" {
			return "", nil, transcriptstore.ErrEventConflict
		}
		resultID, err := transcriptstore.AskUserResultClientMessageIDV1(discussion.Origin)
		if err != nil || resultID != discussion.ResultClientID {
			return "", nil, transcriptstore.ErrEventConflict
		}
		var terminalRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload_json FROM transcript_events WHERE stream_uid=? AND client_message_id=? AND event_type=?`, expected.StreamUID, resultID, transcriptstore.AskUserResultEventType).Scan(&terminalRaw); err != nil {
			return "", nil, err
		}
		terminal, err := transcriptstore.DecodeAskUserResultEventV1(terminalRaw)
		resolution, resolutionErr := kernelChildDiscussionResolution(message, discussion.Origin)
		if err != nil || resolutionErr != nil || terminal.Origin != discussion.Origin || terminal.Result.Status != transcriptstore.AskUserStatusDiscussed || terminal.Result.Message != strings.TrimSpace(message.Message) || terminal.ModelContinuation != resolution.ModelContinuation {
			return "", nil, transcriptstore.ErrEventConflict
		}
		data[kernelParentDiscussionKey] = discussion
	}
	if kind == "user_input_response" {
		return "input_response", data, nil
	}
	if kind == "user_message" {
		return "task_intent", data, nil
	}
	return "", nil, transcriptstore.ErrEventConflict
}
