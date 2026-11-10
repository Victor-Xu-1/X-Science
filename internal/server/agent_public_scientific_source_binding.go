package server

import (
	"context"
	"encoding/json"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

func agentPublicScientificSourceEventPayload(event transcriptstore.ProjectedEvent) []byte {
	if len(event.ResolvedPayloadJSON) != 0 {
		return event.ResolvedPayloadJSON
	}
	return event.Event.PayloadJSON
}

func agentPublicScientificSourceEventBinding(callID string, event transcriptstore.ProjectedEvent) agentPublicScientificSourceBinding {
	return agentPublicScientificSourceBinding{ToolCallID: callID, EventID: event.Event.EventID,
		PayloadSHA256: scientificSourceDigest(agentPublicScientificSourceEventPayload(event))}
}

// Fresh selection and immutable replay use the same completed-source proof.
// A selection receipt is only a parent association, never a new source result.
func (s *Server) agentPublicScientificCompletedSourceBinding(ctx context.Context, ownerID string, snapshot transcriptstore.ProjectionSnapshot, event transcriptstore.ProjectedEvent, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, bool, error) {
	if event.Event.Type != "runner_checkpoint" {
		return agentPublicScientificSourceBinding{}, false, nil
	}
	payload := agentPublicScientificSourceEventPayload(event)
	var envelope struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
	}
	if json.Unmarshal(payload, &envelope) == nil && envelope.Schema == agentPublicScientificAcquisitionSchema {
		if envelope.Kind == nativeScientificSourceSelectionKind {
			return agentPublicScientificSourceBinding{}, false, nil
		}
		binding, _, matched, err := s.nativeScientificAcquisitionSource(ctx, snapshot, event, request.SourceURL)
		return binding, matched, err
	}
	var checkpoint sessionRunnerDurableToolCheckpoint
	if json.Unmarshal(payload, &checkpoint) != nil || !strings.EqualFold(strings.TrimSpace(checkpoint.ToolPhase), "completed") {
		return agentPublicScientificSourceBinding{}, false, nil
	}
	content, err := s.agentPublicScientificSourceResult(ctx, ownerID, checkpoint.ToolResult)
	if err != nil {
		return agentPublicScientificSourceBinding{}, false, err
	}
	binding, matched := s.agentPublicScientificCompletedCheckpointBinding(checkpoint, content, event, request)
	return binding, matched, nil
}

func (s *Server) agentPublicScientificCompletedCheckpointBinding(checkpoint sessionRunnerDurableToolCheckpoint, content any, event transcriptstore.ProjectedEvent, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, bool) {
	if agentPublicScientificReadWindowAuthorizesDownload(checkpoint, content, request.SourceURL) {
		return agentPublicScientificSourceEventBinding(checkpoint.ToolCallID, event), true
	}
	if !s.sessionRunnerDurableCheckpointEvidenceTool(checkpoint) || !agentPublicScientificResultAuthorizesDownload(content, request.SourceURL) {
		return agentPublicScientificSourceBinding{}, false
	}
	binding := agentPublicScientificSourceEventBinding(checkpoint.ToolCallID, event)
	if checkpoint.ToolName == "web_fetch" {
		canonical, hosts, verified := agentPublicScientificWebFetchRedirectBinding(content)
		var executedInput struct {
			URL string `json:"url"`
		}
		if verified && agentPublicScientificURLsEquivalent(canonical, request.SourceURL) &&
			json.Unmarshal(sessionRunnerDurableExecutedToolInput(checkpoint), &executedInput) == nil && executedInput.URL == canonical {
			binding.RedirectHosts = hosts
		}
	}
	return binding, true
}
