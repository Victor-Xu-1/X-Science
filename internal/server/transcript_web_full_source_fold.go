package server

import (
	"context"
	"fmt"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Read the captured prefix once. Each existing reduction owns its event slice
// and normalization state; no third raw-history buffer or payload copy is kept.
func (s *Server) projectTranscriptWebHistoryPrefix(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID, sessionID string,
	snapshot transcriptstore.ProjectionSnapshot,
) ([]map[string]any, transcriptWebProjectionSourceMetadata, error) {
	chain, err := transcriptWebSourceEventChain(nil)
	if err != nil {
		return nil, transcriptWebProjectionSourceMetadata{}, err
	}
	metadataEvents := make([]transcriptstore.ProjectedEvent, 0, 128)
	messages, _, err := s.projectTranscriptWebHistoryWithVisitor(ctx, stream, ownerID, sessionID, 1,
		func(visit func(transcriptstore.ProjectedEvent) error) (transcriptstore.ProjectionSnapshot, error) {
			messageVisit := s.transcriptWebProjectionVisitor(ctx, ownerID, visit)
			err := s.visitTranscriptWebProjectionPrefixRaw(ctx, stream, ownerID, snapshot, false,
				func(raw transcriptstore.ProjectedEvent) error {
					// Hash resolved source bytes before either presentation
					// consumer replaces a payload during normalization.
					var err error
					chain, err = extendTranscriptWebSourceEventChain(chain, []transcriptstore.ProjectedEvent{raw})
					if err != nil {
						return err
					}
					metadataEvent := raw
					// Preserve the former coordinate reader's shape. Only the
					// message consumer needs expanded artifact references.
					metadataEvent.ArtifactReferences = []transcriptstore.ArtifactReference{}
					metadataEvents = append(metadataEvents, metadataEvent)
					return messageVisit(raw)
				})
			return snapshot, err
		})
	if err != nil {
		return nil, transcriptWebProjectionSourceMetadata{}, err
	}
	noteTranscriptWebReadStage(ctx, webReadMetadata)
	typedRichHistory, err := s.transcriptStore.HasActiveTypedHistoryBootstrap(ctx, stream.UID, ownerID, stream.Epoch)
	if err != nil {
		return nil, transcriptWebProjectionSourceMetadata{}, err
	}
	metadataEvents, err = s.normalizeNamespaceAskUserProjectionEvents(ctx, ownerID, metadataEvents)
	if err != nil {
		return nil, transcriptWebProjectionSourceMetadata{}, err
	}
	metadata, err := reduceTranscriptWebSourceMetadata(ctx, sessionID, typedRichHistory, chain, metadataEvents)
	if err != nil {
		return nil, transcriptWebProjectionSourceMetadata{}, fmt.Errorf("source metadata: %w", err)
	}
	return messages, metadata, nil
}

// These are the original metadata normalization and reduction steps. The
// caller supplies its own namespace-normalized events and original raw chain.
func reduceTranscriptWebSourceMetadata(
	ctx context.Context, sessionID string, typedRichHistory bool, eventChain string,
	projectedEvents []transcriptstore.ProjectedEvent,
) (transcriptWebProjectionSourceMetadata, error) {
	projectedEvents, err := normalizeTranscriptWebProjectionEvents(projectedEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	assistantSourceEvents := append([]transcriptstore.ProjectedEvent(nil), projectedEvents...)
	projectedEvents, err = preserveTranscriptTerminalFailureCandidates(projectedEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	typedFrameReferences, err := transcriptTypedAskUserFrameReferences(projectedEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	reducer := newTranscriptWebCoordinateReducer(sessionID, typedRichHistory)
	for _, projected := range projectedEvents {
		if err := ctx.Err(); err != nil {
			return transcriptWebProjectionSourceMetadata{}, err
		}
		if projected.Event.FrameEventID != nil && typedFrameReferences[*projected.Event.FrameEventID] {
			continue
		}
		if err := reducer.consume(projected); err != nil {
			return transcriptWebProjectionSourceMetadata{}, fmt.Errorf("projection event id=%d type=%s attempt=%v: %w", projected.Event.EventID, projected.Event.Type, projected.Event.RunnerAttempt, err)
		}
	}
	coordinates, ranges, err := reducer.finalizeWithPublicationRanges()
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	assistant, err := transcriptWebIncrementalAssistantStateFromReducer(reducer, assistantSourceEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	return transcriptWebProjectionSourceMetadata{
		coordinates: coordinates,
		ranges:      ranges,
		eventChain:  eventChain,
		assistant:   assistant,
	}, nil
}
