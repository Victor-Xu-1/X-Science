package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"
	"reflect"
	"strings"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

const hostGrantContextChangedNotice = "Local directory authorization has changed. Refresh list_host_grants before the next local file operation; previous directory results are historical, not current permission."
const hostGrantContextMissingNotice = "Current directory discovery is unavailable in this model context. Call list_host_grants before the next local file operation. Its result is discovery, not permission to bypass runtime access checks."

// This projection owns no cache, receipt, retry, or permission state. Only the
// canonical completion of list_host_grants acknowledges a fresh discovery.
// Provider errors, preparing/queuing a request, and emitting a notice consume
// nothing. Rechecking the actual request also detects compaction losing its
// directory result even when the retained historical receipt is still current.
type runnerHostGrantContextClient struct {
	server   *Server
	run      *sessionRunnerChatRun
	delegate agentruntime.ModelClient
}

func (client runnerHostGrantContextClient) prepare(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelRequest, error) {
	if err := ctx.Err(); err != nil {
		return request, err
	}
	if client.server == nil || client.run == nil || client.run.Transcript == nil || client.run.frameOwnedJob ||
		ctx.Value(auxiliaryContextUsageKey{}) == true {
		return request, nil
	}
	claim := client.run.Transcript.Claim
	if claim.ResumeSource != transcriptstore.ResumeSourceFresh && claim.ResumeSource != transcriptstore.ResumeSourceUserInput {
		return request, nil
	}
	available := false
	for _, tool := range request.Tools {
		if tool.Name == listHostGrantsToolName {
			available = true
			break
		}
	}
	if !available {
		return request, nil
	}
	discovery, err := client.server.transcriptStore.ReadClaimedToolDiscovery(ctx, claim, listHostGrantsToolName)
	if err != nil {
		return request, err
	}
	input := discovery.Input
	if input == nil || input.Association == nil || input.Association.InputRevision != claim.ClaimedInputRevision ||
		transcriptstore.HiddenConversationControlIntent(map[string]any{"inputData": input.InputData}) != "" {
		return request, nil
	}
	notice := hostGrantContextMissingNotice
	var current map[string]any
	if client.server.settingsStore != nil {
		grants, readErr := client.server.loadHostGrants(claim.OwnerID)
		if readErr == nil {
			current, readErr = hostGrantSnapshotResult(claim.OwnerID, grants)
		}
		if readErr != nil {
			// Optional context cannot become a gate on unrelated computation.
			// Keep unknown explicit; the real discovery/access tools retain their
			// normal errors and never inherit authority from this reminder.
			log.Printf("directory discovery context unavailable attempt=%d err_type=%T", claim.Attempt, readErr)
		}
	}
	var checkpoint map[string]any
	if len(discovery.PayloadJSON) > 0 && json.Unmarshal(discovery.PayloadJSON, &checkpoint) == nil &&
		checkpoint["status"] == "completed" && checkpoint["toolPhase"] == "completed" && checkpoint["rejectedBeforeExecution"] != true &&
		agentruntime.ClassifyToolResult(checkpoint["toolResult"]) == agentruntime.ToolResultSucceeded {
		result, retainedNotice := client.discoveryContextResult(ctx, discovery, checkpoint)
		if retainedNotice != "" {
			notice = retainedNotice
		}
		revision, _ := result["snapshotRevision"].(string)
		if current != nil && validHostGrantSnapshotRevision(revision) && revision != current["snapshotRevision"] {
			notice = hostGrantContextChangedNotice
		} else if retainedNotice == "" && current != nil && validHostGrantSnapshotRevision(revision) &&
			hostGrantDiscoveryInRequest(request, stringValue(checkpoint["toolCallId"]), result) {
			if err := client.validateInput(ctx, input); err != nil {
				return request, err
			}
			return request, nil
		}
	}
	if err := client.validateInput(ctx, input); err != nil {
		return request, err
	}
	request.Messages = append(append([]agentruntime.Message(nil), request.Messages...), agentruntime.Message{
		Role: "system", Content: notice, ContextUsageSource: agentruntime.ContextUsageSystemPrompt,
	})
	return request, nil
}

func (client runnerHostGrantContextClient) validateInput(ctx context.Context, prepared *transcriptstore.ClaimedInputContext) error {
	current, err := client.server.transcriptStore.ClaimedRunnerInputContext(ctx, client.run.Transcript.Claim)
	if err != nil {
		return err
	}
	if current == nil || current.Association == nil || current.EventID != prepared.EventID ||
		current.PublicationSeq != prepared.PublicationSeq || current.ClientMessageID != prepared.ClientMessageID {
		return transcriptstore.ErrClaimStale
	}
	a, b := *current.Association, *prepared.Association
	// A legitimate newly queued input advances the read cutoff but does not
	// replace this claim's admitted input or invalidate its prepared context.
	a.Scope.ThroughPublicationSequence = 0
	b.Scope.ThroughPublicationSequence = 0
	if a != b {
		return transcriptstore.ErrClaimStale
	}
	return ctx.Err()
}

func validHostGrantSnapshotRevision(revision string) bool {
	if len(revision) != len("sha256:")+64 || !strings.HasPrefix(revision, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(revision, "sha256:"))
	return err == nil
}

func hostGrantDiscoveryInRequest(request agentruntime.ModelRequest, callID string, expected map[string]any) bool {
	if strings.TrimSpace(callID) == "" {
		return false
	}
	for _, message := range request.Messages {
		if message.Role != "tool" || message.ToolCallID != callID {
			continue
		}
		var result map[string]any
		if json.Unmarshal([]byte(message.Content), &result) == nil && reflect.DeepEqual(result, expected) &&
			agentruntime.ClassifyToolResult(result) == agentruntime.ToolResultSucceeded {
			return true
		}
	}
	return false
}

func (client runnerHostGrantContextClient) Complete(ctx context.Context, request agentruntime.ModelRequest) (agentruntime.ModelResponse, error) {
	request, err := client.prepare(ctx, request)
	if err != nil {
		return agentruntime.ModelResponse{}, err
	}
	return client.delegate.Complete(ctx, request)
}

func (client runnerHostGrantContextClient) CompleteStream(ctx context.Context, request agentruntime.ModelRequest, emit func(agentruntime.ModelStreamEvent) error) (agentruntime.ModelResponse, error) {
	request, err := client.prepare(ctx, request)
	if err != nil {
		return agentruntime.ModelResponse{}, err
	}
	if streaming, ok := client.delegate.(agentruntime.StreamingModelClient); ok {
		return streaming.CompleteStream(ctx, request, emit)
	}
	return client.delegate.Complete(ctx, request)
}
