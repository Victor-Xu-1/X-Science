package server

import (
	"context"
	"time"
)

const sessionRunnerCommunicationMetadataTimeout = 25 * time.Millisecond

// Cadence and audit samples are optional projections, not execution evidence.
// Keep their I/O joined to the caller without holding provider delivery or
// cancellation behind an unavailable operational-state store.
func sessionRunnerCommunicationMetadataContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, sessionRunnerCommunicationMetadataTimeout)
}

func (s *Server) persistSessionRunnerCommunicationMetadata(ctx context.Context, namespace, key string, value any) error {
	writeCtx, cancel := sessionRunnerCommunicationMetadataContext(ctx)
	defer cancel()
	_, err := s.runtimeStore.SetContext(writeCtx, namespace, key, value)
	return err
}
