package server

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
)

func (s *Server) projectTranscriptWebHistoryPrefix(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID, sessionID string,
	snapshot transcriptstore.ProjectionSnapshot,
) ([]map[string]any, error) {
	messages, _, err := s.projectTranscriptWebHistoryWithVisitor(ctx, stream, ownerID, sessionID, 1,
		func(visit func(transcriptstore.ProjectedEvent) error) (transcriptstore.ProjectionSnapshot, error) {
			if err := s.visitTranscriptWebProjectionPrefix(ctx, stream, ownerID, snapshot, false, visit); err != nil {
				return transcriptstore.ProjectionSnapshot{}, err
			}
			return snapshot, nil
		})
	return messages, err
}

func (s *Server) transcriptWebProjectionVisitor(
	ctx context.Context,
	ownerID string,
	visit func(transcriptstore.ProjectedEvent) error,
) func(transcriptstore.ProjectedEvent) error {
	normalizer := newTranscriptWebRuntimeDrainSegmentNormalizer()
	return func(projected transcriptstore.ProjectedEvent) error {
		var namespaceErr error
		projected, namespaceErr = s.normalizeNamespaceAskUserProjection(ctx, ownerID, projected)
		if namespaceErr != nil {
			return namespaceErr
		}
		normalized, err := normalizer.normalize(projected)
		if err != nil {
			return err
		}
		return visit(normalized)
	}
}

func (s *Server) visitTranscriptWebProjectionSnapshot(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID string,
	snapshot transcriptstore.ProjectionSnapshot,
	coordinateOnly bool,
	visit func(transcriptstore.ProjectedEvent) error,
) error {
	return s.visitTranscriptWebProjectionSnapshotRaw(ctx, stream, ownerID, snapshot, coordinateOnly,
		s.transcriptWebProjectionVisitor(ctx, ownerID, visit))
}

// Strict snapshot consumers still require the exact head. Only the full
// projection owner uses the separately source/CAS-fenced prefix entry below.
func (s *Server) visitTranscriptWebProjectionSnapshotRaw(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID string,
	snapshot transcriptstore.ProjectionSnapshot,
	coordinateOnly bool,
	visit func(transcriptstore.ProjectedEvent) error,
) error {
	current, err := s.visitTranscriptWebProjectionSnapshotPages(ctx, stream, ownerID, snapshot, coordinateOnly, visit)
	if err != nil {
		return err
	}
	if current.ThroughPublicationSequence != snapshot.ThroughPublicationSequence {
		return transcriptstore.ErrBranchStateStale
	}
	return nil
}

func (s *Server) visitTranscriptWebProjectionPrefix(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID string,
	snapshot transcriptstore.ProjectionSnapshot,
	coordinateOnly bool,
	visit func(transcriptstore.ProjectedEvent) error,
) error {
	return s.visitTranscriptWebProjectionPrefixRaw(ctx, stream, ownerID, snapshot, coordinateOnly,
		s.transcriptWebProjectionVisitor(ctx, ownerID, visit))
}

// Prefix traversal proves owner/branch/generation and bounded event membership,
// not current readiness or source revision. Its full owner must bind the latter
// atomically when applying the resulting projection.
func (s *Server) visitTranscriptWebProjectionPrefixRaw(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID string,
	snapshot transcriptstore.ProjectionSnapshot,
	coordinateOnly bool,
	visit func(transcriptstore.ProjectedEvent) error,
) error {
	_, err := s.visitTranscriptWebProjectionSnapshotPages(ctx, stream, ownerID, snapshot, coordinateOnly, visit)
	return err
}

func (s *Server) visitTranscriptWebProjectionSnapshotPages(
	ctx context.Context,
	stream transcriptstore.Stream,
	ownerID string,
	snapshot transcriptstore.ProjectionSnapshot,
	coordinateOnly bool,
	visit func(transcriptstore.ProjectedEvent) error,
) (transcriptstore.ProjectionSnapshot, error) {
	if snapshot.StreamUID != stream.UID || snapshot.BranchID == "" || snapshot.BranchGeneration <= 0 ||
		snapshot.ThroughPublicationSequence < 0 {
		return transcriptstore.ProjectionSnapshot{}, transcriptstore.ErrEventConflict
	}
	const pageSize = 1000
	after := int64(0)
	for after < snapshot.ThroughPublicationSequence {
		if err := ctx.Err(); err != nil {
			return transcriptstore.ProjectionSnapshot{}, err
		}
		input := transcriptstore.ListProjectedEventsInput{
			StreamUID: stream.UID, OwnerID: ownerID,
			BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
			AfterPublicationSequence:   after,
			ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: pageSize,
		}
		var page []transcriptstore.ProjectedEvent
		var err error
		if coordinateOnly {
			page, err = s.transcriptStore.ListProjectedCoordinateEvents(ctx, input)
		} else {
			page, err = s.transcriptStore.ListProjectedEvents(ctx, input)
		}
		if err != nil {
			return transcriptstore.ProjectionSnapshot{}, err
		}
		if len(page) == 0 {
			return transcriptstore.ProjectionSnapshot{}, transcriptstore.ErrEventConflict
		}
		for _, projected := range page {
			if err := ctx.Err(); err != nil {
				return transcriptstore.ProjectionSnapshot{}, err
			}
			if err := visit(projected); err != nil {
				return transcriptstore.ProjectionSnapshot{}, err
			}
		}
		next := page[len(page)-1].Event.PublicationSeq
		if next <= after || next > snapshot.ThroughPublicationSequence {
			return transcriptstore.ProjectionSnapshot{}, transcriptstore.ErrEventConflict
		}
		after = next
	}
	current, err := s.transcriptStore.GetBranchProjectionSnapshot(ctx, stream.UID, ownerID, snapshot.BranchID)
	if err != nil {
		return transcriptstore.ProjectionSnapshot{}, err
	}
	if current.BranchID != snapshot.BranchID || current.BranchGeneration != snapshot.BranchGeneration ||
		current.ThroughPublicationSequence < snapshot.ThroughPublicationSequence {
		return transcriptstore.ProjectionSnapshot{}, transcriptstore.ErrBranchStateStale
	}
	return current, nil
}
