package server

import (
	"context"
	transcriptstore "synon-go/internal/persistence/transcript"
	"time"
)

// Renewal is retryable only inside the currently owned lease. The immutable
// claim identity is never rewritten by the heartbeat goroutine; the store is
// the authority for expiry and ownership after every attempted write.
func (s *Server) renewTranscriptRunnerLease(ctx context.Context, claim transcriptstore.RunnerClaim, ttl time.Duration, expiresAt time.Time) (transcriptstore.HeartbeatRunnerResult, error) {
	// Use the remaining already-owned grant, not a shorter preparation slice.
	// A temporary writer/acquisition delay must not kill live work while that
	// authority is still valid. Parent cancellation and durable expiry remain
	// hard bounds, and the store rechecks all fences after obtaining its lock.
	deadline := time.Now().Add(ttl)
	if !expiresAt.IsZero() && expiresAt.Before(deadline) {
		deadline = expiresAt
	}
	renewalCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var result transcriptstore.HeartbeatRunnerResult
	err := retryTransientStoreContention(renewalCtx, "runner_lease_renewal", func() error {
		var err error
		result, err = s.transcriptStore.HeartbeatRunner(renewalCtx, transcriptstore.HeartbeatRunnerInput{Claim: claim, TTL: ttl})
		return err
	})
	return result, err
}
