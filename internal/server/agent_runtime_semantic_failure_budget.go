package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"synon-go/internal/agentruntime"
	transcriptstore "synon-go/internal/persistence/transcript"
)

// The adapter only decodes canonical terminal receipts. All retry decisions
// belong to the same ledger used by the live engine.
type durableSemanticFailureReducer struct {
	state agentruntime.ExecutionFailureLedger
	call  agentruntime.ToolCall
}

func newDurableSemanticFailureReducer(toolName string, arguments json.RawMessage) *durableSemanticFailureReducer {
	call := agentruntime.ToolCall{Name: strings.TrimSpace(toolName), Arguments: arguments}
	return &durableSemanticFailureReducer{call: call, state: agentruntime.ExecutionFailuresForCall(call)}
}

func (reducer *durableSemanticFailureReducer) observe(event transcriptstore.Event, payload []byte) {
	if event.Type == "user_message" || event.Type == "history_user_message" {
		reducer.state.Reset()
		return
	}
	if event.Type != "runner_checkpoint" || len(payload) == 0 {
		return
	}
	message := map[string]any{}
	if json.Unmarshal(payload, &message) != nil {
		return
	}
	phase := strings.ToLower(strings.TrimSpace(stringValue(message["toolPhase"])))
	status := strings.ToLower(strings.TrimSpace(stringValue(message["status"])))
	result, ok := message["toolResult"].(map[string]any)
	code := strings.ToLower(strings.TrimSpace(stringValue(result["code"])))
	if !ok || code == "execution_path_exhausted" || code == "semantic_failure_retry_exhausted" ||
		(phase != "failed" && status != "failed" && phase != "completed" && status != "completed") {
		return
	}
	toolInput, _ := message["toolInput"].(map[string]any)
	inputJSON, _ := json.Marshal(toolInput)
	reducer.state.Observe(agentruntime.ToolCall{
		Name: strings.TrimSpace(stringValue(message["toolName"])), Arguments: inputJSON,
	}, stringArrayValue(message["toolCapabilities"]), result)
}

func (g serverAgentRuntimeToolGateway) durableSemanticFailureBoundary(
	ctx context.Context,
	toolName string,
	input map[string]any,
) (map[string]any, error) {
	if g.server == nil || g.server.transcriptStore == nil || strings.TrimSpace(g.sessionID) == "" {
		return nil, nil
	}
	arguments, _ := json.Marshal(input)
	stream, found, err := g.server.transcriptStore.GetFrameStreamBySession(ctx, "local", strings.TrimSpace(g.sessionID))
	if err != nil {
		return nil, fmt.Errorf("read execution history stream: %w", err)
	}
	if !found {
		return nil, nil
	}
	snapshot, err := g.server.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("read execution history snapshot: %w", err)
	}
	// The active transcript is the sole durable authority. A separate failure-
	// only cache cannot observe successful repairs or user-turn boundaries.
	// Execution receipts are not provider messages: the current admitted batch
	// is necessarily open here. Stream the fenced event projection so that model
	// compaction and replay seed limits cannot erase older failures or repairs.
	reducer := newDurableSemanticFailureReducer(toolName, arguments)
	err = g.server.scanTranscriptProjection(ctx, snapshot, stream.OwnerID, snapshot.ThroughPublicationSequence,
		func(projected transcriptstore.ProjectedEvent) error {
			reducer.observe(projected.Event, projected.ResolvedPayloadJSON)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read execution history receipts: %w", err)
	}
	return reducer.state.Boundary(reducer.call), nil
}

// Marks authority-read failures at the gateway before execution can begin.
func durableSemanticPreflightBoundary(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	value["executed"] = false
	value["preflight"] = true
	return value
}
