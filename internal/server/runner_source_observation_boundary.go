package server

import (
	"context"
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Find the immutable admitted-input coordinate before opening source blobs.
// Counting mutable attempts or inferring revisions from text cannot substitute
// for this boundary. Live and recovery use the same pinned canonical snapshot.
func (s *Server) runnerSourceObservationInputBoundary(ctx context.Context, authority *transcriptRunnerAuthority, snapshot transcriptstore.ProjectionSnapshot) (int64, error) {
	var after, boundary int64
	for after < snapshot.ThroughPublicationSequence {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		page, err := s.transcriptStore.ListProjectedCoordinateEvents(ctx, transcriptstore.ListProjectedEventsInput{
			StreamUID: authority.Stream.UID, OwnerID: authority.Stream.OwnerID, BranchID: snapshot.BranchID,
			BranchGeneration: snapshot.BranchGeneration, AfterPublicationSequence: after,
			ThroughPublicationSequence: snapshot.ThroughPublicationSequence, Limit: 128,
		})
		if err != nil {
			return 0, err
		}
		if len(page) == 0 {
			return 0, fmt.Errorf("source input boundary projection ended before snapshot")
		}
		for _, projected := range page {
			after = projected.Event.PublicationSeq
			if projected.Event.Type == "user_message" || projected.Event.Type == "user_input_response" {
				boundary = after
			}
		}
	}
	return boundary, nil
}
