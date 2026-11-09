package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	workspace "x-science/internal/persistence/workspace"
)

const toolArgumentSourceRaw = `{"command":"FIRST_SOURCE_ARGUMENT","command":"SECOND_SOURCE_ARGUMENT"}`

type toolArgumentSourceModel struct {
	responses []agentruntime.ModelResponse
	requests  []agentruntime.ModelRequest
}

func (model *toolArgumentSourceModel) Complete(_ context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	model.requests = append(model.requests, agentruntime.SnapshotModelRequest(request))
	index := len(model.requests) - 1
	if index >= len(model.responses) {
		return agentruntime.ModelResponse{}, errors.New("synthetic response schedule exhausted")
	}
	return model.responses[index], nil
}

func TestEngineRejectedToolArgumentsSourceSurvivesCheckpointReopen(t *testing.T) {
	fixture := newCorrectionRouteFixture(t)
	ctx := context.Background()
	options := SessionRunnerChatOptions{RunnerID: fixture.claim.RunnerID}
	model := &toolArgumentSourceModel{}
	for index := 1; index <= 4; index++ {
		model.responses = append(model.responses, toolArgumentSourceResponse(fmt.Sprintf("source-%d", index), toolArgumentSourceRaw))
	}
	stopAfterCheckpoint := errors.New("synthetic stop after the rejection checkpoint")
	var checkpointCall agentruntime.ToolCall
	executions, boundaries := 0, 0
	engine := agentruntime.Engine{
		Model: model,
		Tools: agentruntime.FuncToolGateway(func(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
			executions++
			return agentruntime.ToolResult{}, errors.New("rejected proposal reached the synthetic gateway")
		}),
		OnEventError: func(event agentruntime.Event) error {
			if event.Type != agentruntime.EventModelResponse || len(event.ToolCalls) == 0 {
				return nil
			}
			boundaries++
			if len(event.ToolCalls) != 1 || event.ToolCalls[0].ID != "source-4" || !event.ToolCalls[0].RejectedBeforeExecution {
				return errors.New("private repair did not produce the fourth bounded rejection")
			}
			checkpointCall = event.ToolCalls[0]
			if err := fixture.server.checkpointChatModelToolCallsWithContext(ctx, options, fixture.run, event.ToolCalls); err != nil {
				return err
			}
			return stopAfterCheckpoint
		},
	}
	_, err := engine.Run(ctx, toolArgumentSourceRequest())
	if !errors.Is(err, stopAfterCheckpoint) || executions != 0 || boundaries != 1 || len(model.requests) != 4 {
		t.Fatalf("unexpected pre-execution boundary: err=%v executions=%d checkpoints=%d requests=%d", err, executions, boundaries, len(model.requests))
	}
	assertToolArgumentSourceIdentity(t, checkpointCall, toolArgumentSourceRaw)
	assertToolArgumentSourcePrivateJSON(t, checkpointCall)
	if string(checkpointCall.Arguments) != "{}" {
		t.Fatal("defaults changed the rejected proposal's inert object")
	}
	batchID := fixture.run.ToolBatchIDs[checkpointCall.ID]
	if batchID == "" {
		t.Fatal("the Engine rejection did not produce a durable batch")
	}
	batch, found, err := fixture.store.GetToolCallBatch(ctx, fixture.stream.OwnerID, batchID)
	if err != nil || !found {
		t.Fatalf("canonical source batch unavailable: found=%t err=%v", found, err)
	}
	var checkpoint []byte
	if err := fixture.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, fixture.stream.UID, batch.SourceEventID).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	assertToolArgumentSourceCheckpoint(t, checkpoint, checkpointCall.ID, toolArgumentSourceRaw)
	beforeReopen := fixture.run
	fixture.reopen(t)
	run := &sessionRunnerChatRun{
		SessionID: beforeReopen.SessionID, Attempt: beforeReopen.Attempt, ClaimToken: beforeReopen.ClaimToken,
		Transcript: beforeReopen.Transcript, AfterEventID: beforeReopen.AfterEventID,
	}
	if resumed, err := fixture.server.resumePendingAgentToolCalls(ctx, options, run); err != nil || !resumed {
		t.Fatalf("reopening did not settle the original rejection: resumed=%t err=%v", resumed, err)
	}
	boundary := run.AfterEventID
	if resumed, err := fixture.server.resumePendingAgentToolCalls(ctx, options, run); err != nil || resumed || run.AfterEventID != boundary {
		t.Fatalf("repeated resume created another receipt: resumed=%t err=%v", resumed, err)
	}
	if _, found, err := fixture.store.GetKernelLocalOperationByToolCall(ctx, fixture.stream.OwnerID, fixture.stream.UID, checkpointCall.ID); err != nil || found {
		t.Fatalf("a rejected proposal acquired kernel authority: found=%t err=%v", found, err)
	}
	items, err := fixture.store.ListToolCallBatchItems(ctx, fixture.stream.OwnerID, batchID)
	if err != nil || len(items) != 1 || items[0].StartedEventID != 0 || items[0].WaitingEventID != 0 || items[0].State != workspace.ToolCallBatchItemStateFailed {
		t.Fatalf("the reopened rejection started or waited: item_count=%d err=%v", len(items), err)
	}
	entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := sessionEntriesToProviderMessages("system", entries)
	if err != nil {
		t.Fatal(err)
	}
	feedbackCount := 0
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == checkpointCall.ID && strings.Contains(message.Content, `"executed":false`) && strings.Contains(message.Content, "invalid_tool_arguments") {
			feedbackCount++
		}
	}
	if feedbackCount != 1 {
		t.Fatalf("replay did not retain exactly one rejection receipt: count=%d", feedbackCount)
	}
}

func TestEngineCorrectedToolArgumentsHaveNoRejectedSource(t *testing.T) {
	model := &toolArgumentSourceModel{responses: []agentruntime.ModelResponse{
		toolArgumentSourceResponse("source-invalid", toolArgumentSourceRaw),
		toolArgumentSourceResponse("source-corrected", `{"command":"synthetic corrected input"}`),
		{Message: agentruntime.Message{Role: "assistant", Content: "Synthetic correction completed."}},
	}}
	var executed []agentruntime.ToolCall
	engine := agentruntime.Engine{
		Model: model,
		Tools: agentruntime.FuncToolGateway(func(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
			executed = append(executed, call)
			return agentruntime.ToolResult{Value: map[string]any{"ok": true}}, nil
		}),
	}
	result, err := engine.Run(context.Background(), toolArgumentSourceRequest())
	if err != nil || result.FinalMessage.Content != "Synthetic correction completed." || len(model.requests) != 3 || len(executed) != 1 || executed[0].ID != "source-corrected" {
		t.Fatalf("the corrected proposal did not continue: err=%v requests=%d executions=%d", err, len(model.requests), len(executed))
	}
	if _, _, found := executed[0].OriginalArgumentsSource(); found || executed[0].RejectedBeforeExecution || executed[0].ProviderProtocolDiagnostic != "" {
		t.Fatal("a new valid proposal inherited the rejected proposal's host metadata")
	}
	var arguments map[string]any
	if json.Unmarshal(executed[0].Arguments, &arguments) != nil || arguments["environment"] != "synthetic-default" {
		t.Fatal("the corrected proposal lost its valid default normalization")
	}
	foundSource := false
	for _, message := range model.requests[1].Messages {
		for _, call := range message.ToolCalls {
			if call.ID == "source-invalid" {
				assertToolArgumentSourceIdentity(t, call, toolArgumentSourceRaw)
				assertToolArgumentSourcePrivateJSON(t, call)
				foundSource = true
			}
		}
	}
	if !foundSource {
		t.Fatal("the private repair request lost the original rejected proposal identity")
	}
}

func toolArgumentSourceResponse(id, raw string) agentruntime.ModelResponse {
	return agentruntime.ModelResponse{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: id, Name: "bash", Arguments: json.RawMessage(raw),
	}}}}
}

func toolArgumentSourceRequest() agentruntime.RunRequest {
	return agentruntime.RunRequest{
		Messages: []agentruntime.Message{{Role: "user", Content: "Validate synthetic arguments without a real executor."}},
		Tools: []agentruntime.ToolSchema{{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"command":     map[string]any{"type": "string"},
				"environment": map[string]any{"type": "string", "default": "synthetic-default"},
			},
		}}},
		MaxToolRounds: 2,
	}
}

func assertToolArgumentSourceIdentity(t *testing.T, call agentruntime.ToolCall, raw string) {
	t.Helper()
	expected := sha256.Sum256([]byte(raw))
	digest, size, found := call.OriginalArgumentsSource()
	if !found || digest != hex.EncodeToString(expected[:]) || size != len(raw) {
		t.Fatal("the immutable source does not identify the original invalid bytes")
	}
}

func assertToolArgumentSourcePrivateJSON(t *testing.T, call agentruntime.ToolCall) {
	t.Helper()
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if json.Unmarshal(encoded, &public) != nil || len(public) != 3 || public["id"] == nil || public["name"] == nil || public["arguments"] == nil {
		t.Fatal("public ToolCall JSON included host-only source or rejection fields")
	}
	digest, _, _ := call.OriginalArgumentsSource()
	if strings.Contains(string(encoded), digest) || strings.Contains(string(encoded), "SOURCE_ARGUMENT") {
		t.Fatal("public ToolCall JSON exposed the original argument source")
	}
}

func assertToolArgumentSourceCheckpoint(t *testing.T, raw []byte, callID, original string) {
	t.Helper()
	var stored struct {
		Calls []struct {
			ID            string          `json:"id"`
			Arguments     json.RawMessage `json:"arguments"`
			Rejected      bool            `json:"rejectedBeforeExecution"`
			OriginalSHA   string          `json:"originalArgumentsSHA256"`
			OriginalBytes int             `json:"originalArgumentsByteLength"`
			Feedback      struct {
				Executed  *bool  `json:"executed"`
				Retryable *bool  `json:"retryable"`
				Code      string `json:"code"`
			} `json:"preExecutionRejection"`
		} `json:"modelToolCalls"`
	}
	if json.Unmarshal(raw, &stored) != nil || len(stored.Calls) != 1 {
		t.Fatal("the source checkpoint is not a single complete tool proposal")
	}
	call := stored.Calls[0]
	expected := sha256.Sum256([]byte(original))
	if call.ID != callID || !call.Rejected || string(call.Arguments) != "{}" || call.OriginalSHA != hex.EncodeToString(expected[:]) || call.OriginalBytes != len(original) {
		t.Fatal("the Engine-to-checkpoint path lost the rejected proposal source")
	}
	if call.Feedback.Executed == nil || *call.Feedback.Executed || call.Feedback.Retryable == nil || *call.Feedback.Retryable || call.Feedback.Code != "invalid_tool_arguments" {
		t.Fatal("the bounded rejection checkpoint lost its nonexecuted, exhausted feedback")
	}
	if strings.Contains(string(raw), "SOURCE_ARGUMENT") {
		t.Fatal("the durable source copied original invalid argument values")
	}
}
