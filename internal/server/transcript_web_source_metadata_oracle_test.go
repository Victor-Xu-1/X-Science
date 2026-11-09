package server

import (
	"context"
	"fmt"
	transcriptstore "x-science/internal/persistence/transcript"
)

// Original independent two-pass metadata reader retained only as a test oracle.
// Production full projection no longer has a second source-read execution path.
func (s *Server) projectTranscriptWebSourceMetadata(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID, sessionID string,
	snapshot transcriptstore.ProjectionSnapshot,
) (transcriptWebProjectionSourceMetadata, error) {
	typedRichHistory, err := s.transcriptStore.HasActiveTypedHistoryBootstrap(ctx, stream.UID, ownerID, stream.Epoch)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	projectedEvents := make([]transcriptstore.ProjectedEvent, 0, 128)
	if err := s.visitTranscriptWebProjectionPrefixRaw(
		ctx, stream, ownerID, snapshot, true,
		func(projected transcriptstore.ProjectedEvent) error {
			projectedEvents = append(projectedEvents, projected)
			return nil
		},
	); err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	eventChain, err := transcriptWebSourceEventChain(projectedEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	projectedEvents, err = s.normalizeNamespaceAskUserProjectionEvents(ctx, ownerID, projectedEvents)
	if err != nil {
		return transcriptWebProjectionSourceMetadata{}, err
	}
	projectedEvents, err = normalizeTranscriptWebProjectionEvents(projectedEvents)
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
