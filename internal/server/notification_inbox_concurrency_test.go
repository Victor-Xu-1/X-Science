package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	runtimekv "x-science/internal/persistence/runtimekv"
	transcriptstore "x-science/internal/persistence/transcript"
)

const notificationCASOwner = "inbox-cas-owner"

type notificationCASResult struct {
	snapshot notificationInboxSnapshot
	err      error
}

type notificationCASFlight struct {
	loaded    chan struct{}
	release   chan struct{}
	once      sync.Once
	done      chan notificationCASResult
	joined    bool
	calls     int
	positions []map[string]transcriptstore.NotificationSourcePosition
}

func notificationCASFixture(t *testing.T, frames ...string) (*notificationInboxFixture, notificationInboxState) {
	t.Helper()
	f := newNotificationInboxFixture(t)
	for _, frame := range frames {
		f.complete(t, notificationCASOwner, frame)
	}
	readNotificationInbox(t, f, notificationCASOwner)
	state, err := f.app.loadNotificationInbox(notificationCASOwner)
	if err != nil {
		t.Fatal(err)
	}
	return f, state
}

func startNotificationCASFlight(t *testing.T, f *notificationInboxFixture) *notificationCASFlight {
	t.Helper()
	flight := &notificationCASFlight{loaded: make(chan struct{}), release: make(chan struct{}), done: make(chan notificationCASResult, 1)}
	reader := func(ctx context.Context, owner, after string, include bool, positions map[string]transcriptstore.NotificationSourcePosition) (transcriptstore.NotificationSourcePage, error) {
		flight.calls++
		copied := make(map[string]transcriptstore.NotificationSourcePosition, len(positions))
		for key, value := range positions {
			copied[key] = value
		}
		flight.positions = append(flight.positions, copied)
		page, err := f.repo.ReadNotificationSources(ctx, owner, after, include, positions)
		if flight.calls == 1 {
			close(flight.loaded)
			<-flight.release
		}
		return page, err
	}
	go func() {
		snapshot, err := f.app.reconcileNotificationInboxWithReader(t.Context(), notificationCASOwner, "", reader)
		flight.done <- notificationCASResult{snapshot, err}
	}()
	t.Cleanup(func() {
		flight.unblock()
		if !flight.joined {
			flight.await(t)
		}
	})
	select {
	case <-flight.loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("isolated source read did not reach the channel barrier")
	}
	return flight
}

func (flight *notificationCASFlight) unblock() {
	flight.once.Do(func() { close(flight.release) })
}

func (flight *notificationCASFlight) await(t *testing.T) notificationCASResult {
	t.Helper()
	select {
	case result := <-flight.done:
		flight.joined = true
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("isolated reconcile did not join after releasing the channel barrier")
		return notificationCASResult{}
	}
}

func notificationCASRequireSuccess(t *testing.T, label string, result notificationCASResult) {
	t.Helper()
	if result.err != nil {
		response := httptest.NewRecorder()
		writeNotificationInboxError(response, result.err)
		t.Fatalf("%s returned HTTP %d: %s", label, response.Code, response.Body.String())
	}
}

func TestNotificationInboxConcurrentGETReconcilesFreshSources(t *testing.T) {
	f, base := notificationCASFixture(t, "cas-a")
	flight := startNotificationCASFlight(t, f)
	// B has loaded revision N and completed the actual atomic source read.
	// Publish a competing GET, then add a source absent from either old page.
	f.complete(t, notificationCASOwner, "cas-b")
	winner, err := f.app.reconcileNotificationInbox(t.Context(), notificationCASOwner, "")
	if err != nil || winner.Revision != base.Revision+1 || len(winner.Items) != 2 {
		t.Fatalf("competing real GET: %+v err=%v", winner, err)
	}
	f.complete(t, notificationCASOwner, "cas-c")
	flight.unblock()
	result := flight.await(t)
	notificationCASRequireSuccess(t, "overlapping GET", result)
	if flight.calls != 2 || result.snapshot.Revision != winner.Revision+1 || len(result.snapshot.Items) != 3 || result.snapshot.UnreadCount != 3 {
		t.Fatalf("fresh source/state not reconciled: calls=%d snapshot=%+v", flight.calls, result.snapshot)
	}
	if _, fresh := flight.positions[1]["stream-cas-b"]; !fresh {
		t.Fatal("retry reused pre-winner source positions")
	}
	ids := map[string]bool{}
	for _, item := range result.snapshot.Items {
		if ids[item.ID] {
			t.Fatal("overlap duplicated a canonical notification")
		}
		ids[item.ID] = true
	}
}

func TestNotificationInboxConcurrentReadAckRemainsRead(t *testing.T) {
	f, base := notificationCASFixture(t, "ack-a", "ack-b")
	receipt := notificationInboxPublic(base).ReadReceipt
	flight := startNotificationCASFlight(t, f)
	ack, err := f.app.markNotificationInboxRead(notificationCASOwner, nil, receipt)
	if err != nil || ack.UnreadCount != 0 || ack.Revision != base.Revision+1 {
		t.Fatalf("original read-all ack=%+v err=%v", ack, err)
	}
	f.complete(t, notificationCASOwner, "ack-c")
	flight.unblock()
	result := flight.await(t)
	notificationCASRequireSuccess(t, "read-ack race", result)
	if flight.calls != 2 || result.snapshot.UnreadCount != 1 || len(result.snapshot.Items) != 3 {
		t.Fatalf("read ack/new unread lost: calls=%d snapshot=%+v", flight.calls, result.snapshot)
	}
	readIDs := map[string]bool{}
	for _, item := range base.Items {
		readIDs[item.ID] = true
	}
	for _, item := range result.snapshot.Items {
		if readIDs[item.ID] && item.ReadAt == "" {
			t.Fatal("retry resurrected an acknowledged unread item")
		}
	}
	again, err := f.app.markNotificationInboxRead(notificationCASOwner, nil, receipt)
	if err != nil || again.UnreadCount != 1 {
		t.Fatalf("old receipt read a newer item: %+v err=%v", again, err)
	}
}

func TestNotificationInboxCASRefreshStopsAfterTwoAttempts(t *testing.T) {
	f, base := notificationCASFixture(t, "limit-a", "limit-b")
	calls := 0
	reader := func(ctx context.Context, owner, after string, include bool, positions map[string]transcriptstore.NotificationSourcePosition) (transcriptstore.NotificationSourcePage, error) {
		calls++
		page, err := f.repo.ReadNotificationSources(ctx, owner, after, include, positions)
		if calls <= len(base.Items) {
			if _, ackErr := f.app.markNotificationInboxRead(owner, []string{base.Items[calls-1].ID}, ""); ackErr != nil {
				t.Fatal(ackErr)
			}
		}
		return page, err
	}
	_, err := f.app.reconcileNotificationInboxWithReader(t.Context(), notificationCASOwner, "", reader)
	if !errors.Is(err, errNotificationInboxConflict) || calls != 2 {
		t.Fatalf("CAS bound: calls=%d err=%v", calls, err)
	}
	current, err := f.app.loadNotificationInbox(notificationCASOwner)
	if err != nil || current.Revision != base.Revision+2 || notificationInboxPublic(current).UnreadCount != 0 {
		t.Fatalf("losing attempts overwrote the acknowledgements: %+v err=%v", current, err)
	}
}

func TestNotificationInboxCASRefreshHonorsCancellation(t *testing.T) {
	f, base := notificationCASFixture(t, "cancel-a")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	reader := func(ctx context.Context, owner, after string, include bool, positions map[string]transcriptstore.NotificationSourcePosition) (transcriptstore.NotificationSourcePage, error) {
		calls++
		page, err := f.repo.ReadNotificationSources(ctx, owner, after, include, positions)
		if _, ackErr := f.app.markNotificationInboxRead(owner, []string{base.Items[0].ID}, ""); ackErr != nil {
			t.Fatal(ackErr)
		}
		cancel() // The atomic source read completed; cancellation precedes retry.
		return page, err
	}
	_, err := f.app.reconcileNotificationInboxWithReader(ctx, notificationCASOwner, "", reader)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled CAS work continued: calls=%d err=%v", calls, err)
	}
}

func TestNotificationInboxCASRefreshKeepsCursorConflictsStrict(t *testing.T) {
	f, _ := notificationCASFixture(t, "cursor-a", "cursor-b")
	if err := f.runtime.EditNamespace(notificationInboxNamespace(notificationCASOwner), func(entries map[string]runtimekv.Entry) (bool, error) {
		state, err := decodeNotificationInboxState(entries["snapshot"], true, notificationCASOwner)
		if err != nil {
			return false, err
		}
		state.Incomplete = true
		state.Revision++
		return true, storeNotificationInbox(entries, state)
	}); err != nil {
		t.Fatal(err)
	}
	state, err := f.app.loadNotificationInbox(notificationCASOwner)
	if err != nil {
		t.Fatal(err)
	}
	stale := notificationInboxToken(state, "cursor", state.Revision)
	if _, err := f.app.markNotificationInboxRead(notificationCASOwner, []string{state.Items[0].ID}, ""); err != nil {
		t.Fatal(err)
	}
	state, err = f.app.loadNotificationInbox(notificationCASOwner)
	if err != nil {
		t.Fatal(err)
	}
	foreign := state
	foreign.OwnerID = "other-owner"
	for _, tc := range []struct{ name, token string }{{"invalid", "not-a-token"}, {"foreign", notificationInboxToken(foreign, "cursor", state.Revision)}, {"stale", stale}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			reader := func(ctx context.Context, owner, after string, include bool, positions map[string]transcriptstore.NotificationSourcePosition) (transcriptstore.NotificationSourcePage, error) {
				calls++
				return f.repo.ReadNotificationSources(ctx, owner, after, include, positions)
			}
			_, err := f.app.reconcileNotificationInboxWithReader(t.Context(), notificationCASOwner, tc.token, reader)
			if !errors.Is(err, errNotificationInboxConflict) || calls != 0 {
				t.Fatalf("cursor conflict retried: calls=%d err=%v", calls, err)
			}
		})
	}
	t.Run("valid_cursor_lost_publish", func(t *testing.T) {
		calls := 0
		reader := func(ctx context.Context, owner, after string, include bool, positions map[string]transcriptstore.NotificationSourcePosition) (transcriptstore.NotificationSourcePage, error) {
			calls++
			page, err := f.repo.ReadNotificationSources(ctx, owner, after, include, positions)
			if _, ackErr := f.app.markNotificationInboxRead(owner, []string{state.Items[1].ID}, ""); ackErr != nil {
				t.Fatal(ackErr)
			}
			return page, err
		}
		_, err := f.app.reconcileNotificationInboxWithReader(t.Context(), notificationCASOwner, notificationInboxToken(state, "cursor", state.Revision), reader)
		if !errors.Is(err, errNotificationInboxConflict) || calls != 1 {
			t.Fatalf("nonempty cursor publish conflict retried: calls=%d err=%v", calls, err)
		}
	})
}
