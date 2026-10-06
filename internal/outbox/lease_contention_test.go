package outbox

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"synon-go/internal/persistence/workspace"
)

type contendedLeaseRepository struct {
	*workspace.Store
	writer  *sql.Conn
	locked  atomic.Bool
	renewed chan time.Time
}

func (r *contendedLeaseRepository) RenewOutboxClaim(ctx context.Context, id, token string, ttl time.Duration) (time.Time, error) {
	if r.locked.CompareAndSwap(false, true) {
		if _, err := r.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return time.Time{}, err
		}
		go func() {
			time.Sleep(4 * time.Second)
			_, _ = r.writer.ExecContext(context.Background(), "ROLLBACK")
		}()
	}
	expires, err := r.Store.RenewOutboxClaim(ctx, id, token, ttl)
	if err == nil {
		select {
		case r.renewed <- expires:
		default:
		}
	}
	return expires, err
}

func TestLongRunningDispatcherRecoversRealSQLiteWriteContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store := openStoreAt(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	writer, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	event := enqueueEvent(t, store, 1, 8)
	delivery := &leaseTestDeliverer{started: make(chan struct{}), release: make(chan struct{})}
	probe := &contendedLeaseRepository{Store: store, writer: writer, renewed: make(chan time.Time, 2)}
	dispatcher, err := NewDispatcher(probe, delivery, Options{
		WorkerID: "contended-owner", BatchSize: 1, Lease: 9 * time.Second, LongRunning: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("dispatcher did not drain")
		}
	})
	select {
	case expires := <-probe.renewed:
		current, err := store.GetOutboxEvent(t.Context(), event.ID)
		if err != nil || current.LeaseExpiresAt == nil || current.LeaseExpiresAt.Before(expires) || !expires.After(time.Now()) {
			t.Fatalf("renewal did not return committed authority: event=%#v expiry=%v err=%v", current, expires, err)
		}
	case <-time.After(14 * time.Second):
		t.Fatal("write contention terminated valid work")
	}
	if delivery.calls.Load() != 1 || delivery.cancelled.Load() != 0 {
		t.Fatalf("contended work was restarted: calls=%d cancelled=%d", delivery.calls.Load(), delivery.cancelled.Load())
	}
	close(delivery.release)
	waitForOutboxStatus(t, store, event.ID, workspace.OutboxStatusDelivered, 2*time.Second)
}

func TestOutboxRenewalReturnsCommittedDeadline(t *testing.T) {
	store := openStoreAt(t, filepath.Join(t.TempDir(), "workspace.sqlite"))
	event := enqueueEvent(t, store, 1, 8)
	claims, err := store.ClaimOutbox(t.Context(), workspace.ClaimOutboxInput{
		WorkerID: "one-owner", Topics: []string{event.Topic}, Limit: 1, Lease: time.Second,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	expires, err := store.RenewOutboxClaim(t.Context(), event.ID, claims[0].ClaimToken, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.Equal(expires) {
		t.Fatalf("deadline differs from committed authority: expires=%v event=%#v err=%v", expires, current, err)
	}
}
