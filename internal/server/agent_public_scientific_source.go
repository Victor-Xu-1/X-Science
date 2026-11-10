package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	transcriptstore "x-science/internal/persistence/transcript"
)

var errAgentPublicScientificSourceMissing = errors.Join(errAgentPublicScientificFileSource, errors.New("prior source receipt is absent"))

func (s *Server) validateAgentPublicScientificSourceURL(
	ctx context.Context,
	streamUID, ownerID string,
	request agentPublicScientificFileRequest,
) (agentPublicScientificSourceBinding, error) {
	if run, _ := transcriptRunnerChatRunFromContext(ctx); run != nil {
		if source, found := s.registeredExecutionDownloadSource(run, request); found {
			return agentPublicScientificSourceBinding{ToolCallID: source}, nil
		}
	}
	// The selected-run view may be unavailable during a fresh acquisition. An
	// exact match against the local execution-pack catalog is independently
	// authoritative and permits cache reuse without inventing a web source
	// receipt. Arbitrary URLs still take the durable source-evidence path below.
	if _, source, found := s.registeredExecutionDownloadCatalogSource(request); found {
		return agentPublicScientificSourceBinding{ToolCallID: source}, nil
	}
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, streamUID, ownerID)
	if err != nil {
		return agentPublicScientificSourceBinding{}, errors.Join(errAgentPublicScientificFileSource, err)
	}
	requestedSourceToolCallID := request.SourceToolCallID
	for windowEnd := snapshot.ThroughPublicationSequence; windowEnd > 0; {
		windowStart := maxInt64(0, windowEnd-agentPublicScientificReverseWindowSize)
		page, err := s.transcriptStore.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
			StreamUID: streamUID, OwnerID: ownerID, BranchID: snapshot.BranchID,
			BranchGeneration: snapshot.BranchGeneration, AfterPublicationSequence: windowStart,
			ThroughPublicationSequence: windowEnd,
			Limit:                      sessionRunnerDurableEvidencePageSize,
		})
		if err != nil {
			return agentPublicScientificSourceBinding{}, errors.Join(errAgentPublicScientificFileSource, err)
		}
		var fallback agentPublicScientificSourceBinding
		for index := len(page) - 1; index >= 0; index-- {
			projected := page[index]
			if projected.Event.Type != "runner_checkpoint" {
				continue
			}
			payload := projected.ResolvedPayloadJSON
			if len(payload) == 0 {
				payload = projected.Event.PayloadJSON
			}
			// Identify the reserved schema before decoding tool fields. A damaged
			// internal phase stays private, but must not become absent provenance.
			var envelope struct {
				Schema string `json:"schema"`
				Kind   string `json:"kind"`
			}
			if json.Unmarshal(payload, &envelope) == nil && envelope.Schema == agentPublicScientificAcquisitionSchema {
				if envelope.Kind == nativeScientificSourceSelectionKind {
					continue
				}
				binding, _, matched, sourceErr := s.nativeScientificAcquisitionSource(ctx, snapshot, projected, request.SourceURL)
				if sourceErr != nil {
					if fallback.ToolCallID != "" {
						return fallback, nil
					}
					return agentPublicScientificSourceBinding{}, sourceErr
				}
				if matched {
					if requestedSourceToolCallID != "" && binding.ToolCallID == requestedSourceToolCallID {
						return binding, nil
					}
					if fallback.ToolCallID == "" {
						fallback = binding
					}
				}
				continue
			}
			var checkpoint sessionRunnerDurableToolCheckpoint
			if json.Unmarshal(payload, &checkpoint) != nil {
				continue
			}
			// A failed or malformed exact fetch is not an absent source. Do not
			// launder it through an automatic acquisition retry.
			var fetchedInput struct {
				URL string `json:"url"`
			}
			phase := strings.ToLower(strings.TrimSpace(checkpoint.ToolPhase))
			content, err := s.agentPublicScientificSourceResult(ctx, ownerID, checkpoint.ToolResult)
			if checkpoint.ToolName == "web_fetch" &&
				json.Unmarshal(sessionRunnerDurableExecutedToolInput(checkpoint), &fetchedInput) == nil &&
				fetchedInput.URL == request.DownloadURL && (phase == "failed" || phase == "unavailable" || phase == "completed" &&
				(err != nil || !agentPublicScientificResultAuthorizesDownload(content, request.SourceURL))) {
				if fallback.ToolCallID != "" {
					return fallback, nil
				}
				return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
			}
			if phase != "completed" {
				continue
			}
			if err != nil {
				continue
			}
			binding, matched := s.agentPublicScientificCompletedCheckpointBinding(checkpoint, content, projected, request)
			if !matched {
				continue
			}
			// A model-supplied ID is only a preference. The durable checkpoint
			// and exact URL remain the authority, so invented opaque IDs cannot
			// block an otherwise valid download.
			if requestedSourceToolCallID != "" && checkpoint.ToolCallID == requestedSourceToolCallID {
				return binding, nil
			}
			if fallback.ToolCallID == "" {
				fallback = binding
			}
		}
		if fallback.ToolCallID != "" {
			return fallback, nil
		}
		windowEnd = windowStart
	}
	if request.SourceToolCallID != "" {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
	}
	return agentPublicScientificSourceBinding{}, errAgentPublicScientificSourceMissing
}
