package outbox

import (
	"context"
	"errors"
	"time"

	"synon-go/internal/persistence/workspace"
	"synon-go/internal/sqliteutil"
)

type leaseRepository interface {
	RenewOutboxClaim(context.Context, string, string, time.Duration) (time.Time, error)
}

// deliverWithLease keeps a long operation owned without a wall-clock deadline.
// Losing its durable claim cancels execution; the deliverer must finish cleanup
// before this worker can accept more work. Domain receipts fence late results.
func (d *Dispatcher) deliverWithLease(ctx context.Context, event workspace.OutboxEvent) error {
	if event.LeaseExpiresAt == nil || !event.LeaseExpiresAt.After(time.Now()) {
		return workspace.ErrOutboxClaimLost
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		expiresAt := *event.LeaseExpiresAt
		ticker := time.NewTicker(d.lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
			nextExpiry, err := d.renewOwnedClaim(runCtx, event, expiresAt)
			if err != nil {
				cancel(err)
				return
			}
			expiresAt = nextExpiry
		}
	}()
	err := d.deliverer.Deliver(runCtx, event)
	cause := context.Cause(runCtx)
	cancel(nil)
	<-renewDone
	if err != nil && cause != nil && !errors.Is(err, ErrDeliverySettled) {
		return errors.Join(err, cause)
	}
	return err
}

// Recovery is bounded by the remaining owned claim, not heartbeat cadence.
// The repository rechecks the token and expiry after acquiring its write lock.
func (d *Dispatcher) renewOwnedClaim(ctx context.Context, event workspace.OutboxEvent, expiresAt time.Time) (time.Time, error) {
	renewCtx, stop := context.WithDeadline(ctx, expiresAt)
	defer stop()
	delay := max(time.Millisecond, min(d.lease/10, 250*time.Millisecond))
	for {
		if err := renewCtx.Err(); err != nil {
			return time.Time{}, err
		}
		expires, err := d.repository.(leaseRepository).RenewOutboxClaim(renewCtx, event.ID, event.ClaimToken, d.lease)
		if err == nil {
			return expires, nil
		}
		if !sqliteutil.IsTransientContention(err) && !errors.Is(err, context.DeadlineExceeded) {
			return time.Time{}, err
		}
		// Error callbacks may perform blocking I/O. Never run one inside the
		// remaining ownership window; permanent errors are reported by Run.
		timer := time.NewTimer(delay)
		select {
		case <-renewCtx.Done():
			timer.Stop()
			return time.Time{}, renewCtx.Err()
		case <-timer.C:
		}
	}
}
