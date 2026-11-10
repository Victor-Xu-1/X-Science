package server

import (
	"context"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) resolveAgentPublicScientificDownloadSource(ctx context.Context, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, error) {
	binding, committed, err := s.committedAgentPublicScientificSource(ctx, run, parentID, input, request)
	if committed || err != nil {
		return binding, err
	}
	binding, err = s.validateAgentPublicScientificSourceURL(ctx, run.Authority.Stream.UID, run.Authority.Stream.OwnerID, request)
	if errors.Is(err, errAgentPublicScientificSourceMissing) {
		if err = s.acquireNativeScientificSource(ctx, run, parentID, input, request); err != nil {
			return agentPublicScientificSourceBinding{}, err
		}
		binding, err = s.validateAgentPublicScientificSourceURL(ctx, run.Authority.Stream.UID, run.Authority.Stream.OwnerID, request)
	}
	if err == nil {
		err = s.recordNativeScientificSourceSelection(ctx, run, parentID, input, request, binding)
	}
	return binding, err
}

// Only an existing exact-parent artifact commit can use immutable selection
// replay. Fresh parents still consult the latest successful/failed source.
func (s *Server) committedAgentPublicScientificSource(ctx context.Context, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, bool, error) {
	stream := run.Authority.Stream
	commits, err := s.transcriptStore.ListArtifactCommitReferencesForEvent(ctx, stream.UID, stream.OwnerID, run.SourceEventID)
	if err != nil || len(commits) == 0 {
		return agentPublicScientificSourceBinding{}, false, err
	}
	if len(commits) != 1 || commits[0].ArtifactID != streamArtifactIDFor(stream.UID, request.Filename) ||
		commits[0].Relation != transcriptstore.ArtifactRelationConsumed {
		return agentPublicScientificSourceBinding{}, true, errAgentPublicScientificFileConflict
	}
	if task, _ := transcriptRunnerChatRunFromContext(ctx); task != nil {
		if source, found := s.registeredExecutionDownloadSource(task, request); found {
			return agentPublicScientificSourceBinding{ToolCallID: source}, true, nil
		}
	}
	if _, source, found := s.registeredExecutionDownloadCatalogSource(request); found {
		return agentPublicScientificSourceBinding{ToolCallID: source}, true, nil
	}
	binding, err := s.readCommittedNativeScientificSource(ctx, run, parentID, input, request)
	return binding, true, err
}

func (s *Server) readCommittedNativeScientificSource(ctx context.Context, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest) (agentPublicScientificSourceBinding, error) {
	stream := run.Authority.Stream
	snapshot, err := s.transcriptStore.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return agentPublicScientificSourceBinding{}, err
	}
	parent := nativeScientificSourceParent{StreamUID: stream.UID, OwnerID: stream.OwnerID,
		ParentToolCallID: parentID, ParentSourceEventID: run.SourceEventID}
	started, err := s.nativeScientificStartedParent(ctx, snapshot, parent)
	var original map[string]any
	if err != nil || json.Unmarshal(started.ToolInput, &original) != nil {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileAuthority
	}
	originalHash := scientificSourceDigest(mustMarshalRawMessage(original))
	executedHash := scientificSourceDigest(mustMarshalRawMessage(input))
	explicitSource := ""
	if originalHash == executedHash {
		if originalRequest, err := parseAgentPublicScientificFileRequest(original); err == nil {
			explicitSource = originalRequest.SourceToolCallID
		}
	}
	parentEvent, found, err := s.transcriptStore.GetProjectedEventByID(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, BranchID: snapshot.BranchID,
		BranchGeneration: snapshot.BranchGeneration, ThroughPublicationSequence: snapshot.ThroughPublicationSequence,
	}, run.SourceEventID)
	if err != nil || !found {
		return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileAuthority
	}
	for end := snapshot.ThroughPublicationSequence; end > 0; {
		start := maxInt64(0, end-agentPublicScientificReverseWindowSize)
		page, err := s.transcriptStore.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
			StreamUID: stream.UID, OwnerID: stream.OwnerID, BranchID: snapshot.BranchID,
			BranchGeneration: snapshot.BranchGeneration, AfterPublicationSequence: start,
			ThroughPublicationSequence: end, Limit: sessionRunnerDurableEvidencePageSize,
		})
		if err != nil {
			return agentPublicScientificSourceBinding{}, err
		}
		for index := len(page) - 1; index >= 0; index-- {
			event := page[index]
			if event.Event.Type != "runner_checkpoint" {
				continue
			}
			payload := agentPublicScientificSourceEventPayload(event)
			var envelope struct {
				Schema              string `json:"schema"`
				ParentToolCallID    string `json:"parentToolCallId"`
				ParentSourceEventID int64  `json:"parentSourceEventId"`
			}
			if json.Unmarshal(payload, &envelope) == nil && envelope.Schema == agentPublicScientificAcquisitionSchema &&
				envelope.ParentToolCallID == parentID && envelope.ParentSourceEventID == run.SourceEventID {
				var phase nativeScientificAcquisition
				if json.Unmarshal(payload, &phase) != nil || phase.OwnerID != stream.OwnerID || phase.StreamUID != stream.UID ||
					phase.ParentArgumentsSHA256 != originalHash || phase.ParentExecutedSHA256 != executedHash || phase.SourceURL != request.SourceURL {
					return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileConflict
				}
				if phase.Kind == nativeScientificSourceSelectionKind {
					return s.validateNativeScientificSourceSelection(ctx, snapshot, event, request)
				}
				if phase.Kind != "" {
					return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
				}
				if phase.ToolPhase != "source_acquisition_completed" {
					continue
				}
				binding, _, matched, err := s.nativeScientificAcquisitionSource(ctx, snapshot, event, request.SourceURL)
				if err != nil || !matched {
					return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
				}
				return binding, nil
			}
			// A historically explicit ID can be verified from the immutable
			// unchanged start input and its actual earlier source completion.
			// Historical auto-selection has no such proof and stays closed.
			if explicitSource != "" && event.Event.PublicationSeq < parentEvent.Event.PublicationSeq {
				var source sessionRunnerDurableToolCheckpoint
				if json.Unmarshal(payload, &source) == nil && source.ToolCallID == explicitSource {
					binding, matched, err := s.agentPublicScientificCompletedSourceBinding(ctx, stream.OwnerID, snapshot, event, request)
					if err != nil || !matched {
						return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
					}
					return binding, nil
				}
			}
		}
		end = start
	}
	return agentPublicScientificSourceBinding{}, errAgentPublicScientificFileSource
}
