package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newRevisionStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "settings.json"))
}

func revisionSnapshot(t *testing.T, store *Store, keys ...string) *Snapshot {
	t.Helper()
	snapshot, err := store.SnapshotKeys(context.Background(), keys...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snapshot.Close)
	return snapshot
}

func TestSettingsSnapshotInvalidatesAllRelevantWriters(t *testing.T) {
	for name, mutate := range map[string]func(*Store) error{
		"Set": func(s *Store) error { _, err := s.Set("sources", "changed"); return err },
		"Update": func(s *Store) error {
			_, err := s.Update("sources", func(any, bool) (any, error) { return "changed", nil })
			return err
		},
		"UpdateBatch": func(s *Store) error {
			return s.UpdateBatch(map[string]func(any, bool) (any, error){"sources": func(any, bool) (any, error) { return "changed", nil }})
		},
		"Delete": func(s *Store) error { _, err := s.Delete("sources"); return err },
		"ABA": func(s *Store) error {
			if _, err := s.Set("sources", "temporary"); err != nil {
				return err
			}
			_, err := s.Set("sources", "original")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := newRevisionStore(t)
			if _, err := store.Set("sources", "original"); err != nil {
				t.Fatal(err)
			}
			snapshot := revisionSnapshot(t, store, "sources", "grants")
			if err := mutate(store); err != nil {
				t.Fatal(err)
			}
			called := false
			err := store.TryCompare(context.Background(), snapshot, func() error { called = true; return nil })
			if !errors.Is(err, ErrSnapshotConflict) || called {
				t.Fatalf("stale settings commit = %v, callback=%v", err, called)
			}
		})
	}
	store := newRevisionStore(t)
	snapshot := revisionSnapshot(t, store, "sources", "grants")
	if _, err := store.Set("another-owner", "unrelated"); err != nil {
		t.Fatal(err)
	}
	if err := store.TryCompare(context.Background(), snapshot, func() error { return nil }); err != nil {
		t.Fatalf("unrelated key invalidated this owner's exact two keys: %v", err)
	}
	if err := newRevisionStore(t).TryCompare(context.Background(), snapshot, func() error { return nil }); !errors.Is(err, ErrSnapshotConflict) {
		t.Fatalf("foreign Store accepted snapshot: %v", err)
	}
}

func TestSettingsSnapshotCompareUpdateAndSaveFailure(t *testing.T) {
	store := newRevisionStore(t)
	first := revisionSnapshot(t, store, "sources", "grants")
	second := revisionSnapshot(t, store, "sources", "grants")
	updated, err := store.CompareUpdate(context.Background(), first, "sources", func(any, bool) (any, error) { return []string{"accepted"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Close()
	if _, err := store.CompareUpdate(context.Background(), second, "sources", func(any, bool) (any, error) { return []string{"stale"}, nil }); !errors.Is(err, ErrSnapshotConflict) {
		t.Fatalf("concurrent stale write = %v", err)
	}
	if err := store.TryCompare(context.Background(), updated, func() error { return nil }); err != nil {
		t.Fatalf("accepted write lost its fresh snapshot: %v", err)
	}
	if err := os.Mkdir(store.path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareUpdate(context.Background(), updated, "sources", func(any, bool) (any, error) { return "cannot save", nil }); err == nil {
		t.Fatal("synthetic filesystem save failure was accepted")
	}
	called := false
	if err := store.TryCompare(context.Background(), updated, func() error { called = true; return nil }); !errors.Is(err, ErrSnapshotConflict) || called {
		t.Fatalf("failed save left the old token valid: %v, callback=%v", err, called)
	}
	current, found, err := store.Get("sources")
	if err != nil || !found || current.Value.([]any)[0] != "accepted" {
		t.Fatalf("failed write changed accepted content: %+v, %v", current, err)
	}
}

func TestSettingsSnapshotCancellationAndBusy(t *testing.T) {
	store := newRevisionStore(t)
	snapshot := revisionSnapshot(t, store, "sources", "grants")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := store.TryCompare(ctx, snapshot, func() error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled commit = %v, callback=%v", err, called)
	}
	if _, err := store.SnapshotKeys(ctx, "sources", "grants"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot = %v", err)
	}
	if _, err := store.CompareUpdate(ctx, snapshot, "sources", func(any, bool) (any, error) { called = true; return nil, nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled setting update = %v, callback=%v", err, called)
	}
	store.mu.Lock()
	err := store.TryCompare(context.Background(), snapshot, func() error { called = true; return nil })
	store.mu.Unlock()
	if !errors.Is(err, ErrSnapshotBusy) || called {
		t.Fatalf("busy commit = %v, callback=%v", err, called)
	}
}

func TestSettingsSnapshotMetadataLivesOnlyWithIssuedTokens(t *testing.T) {
	store := newRevisionStore(t)
	for owner := 0; owner < 100; owner++ {
		key := "sources.owner" + strconv.Itoa(owner)
		first, err := store.SnapshotKeys(context.Background(), key, "grants.owner"+strconv.Itoa(owner))
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.SnapshotKeys(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if len(store.revisions) != 2 {
			t.Fatal("snapshots retained unrelated owner metadata")
		}
		first.Close()
		first.Close()
		if len(store.revisions) != 1 {
			t.Fatal("one live token did not retain exactly its key")
		}
		second.Close()
		if store.revisions != nil {
			t.Fatal("closed snapshots retained a long-lived key map")
		}
		if err := store.TryCompare(context.Background(), second, func() error { return nil }); !errors.Is(err, ErrSnapshotConflict) {
			t.Fatal("closed token was accepted after metadata reclamation")
		}
	}
}

func TestSettingsSnapshotCloseDoesNotWaitForFileWriter(t *testing.T) {
	store := newRevisionStore(t)
	snapshot, err := store.SnapshotKeys(context.Background(), "sources", "grants")
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	locked := true
	defer func() {
		if locked {
			store.mu.Unlock()
		}
	}()
	if err := store.TryCompare(context.Background(), snapshot, func() error { return nil }); !errors.Is(err, ErrSnapshotBusy) {
		t.Errorf("held settings writer = %v", err)
	}
	closed := make(chan struct{})
	go func() { snapshot.Close(); close(closed) }()
	returnedWhileHeld := false
	select {
	case <-closed:
		returnedWhileHeld = true
	case <-time.After(time.Second):
	}
	store.mu.Unlock()
	locked = false
	if !returnedWhileHeld {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("snapshot release did not finish after its settings writer")
		}
		t.Error("snapshot cleanup waited on settings file IO after a busy commit")
	}
	store.revisionMu.Lock()
	retained := len(store.revisions)
	store.revisionMu.Unlock()
	if retained != 0 {
		t.Fatal("snapshot cleanup retained unreferenced tombstones")
	}
}

func TestSettingsSnapshotCloseDoesNotCancelAdmittedCommit(t *testing.T) {
	store := newRevisionStore(t)
	snapshot, err := store.SnapshotKeys(context.Background(), "sources", "grants")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	admitted, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- store.TryCompare(ctx, snapshot, func() error {
			close(admitted)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-admitted:
	case <-ctx.Done():
		close(release)
		<-finished
		t.Fatal("commit did not reach its controlled admission point")
	}
	closed := make(chan struct{})
	go func() { snapshot.Close(); close(closed) }()
	closedBeforeRelease := false
	select {
	case <-closed:
		closedBeforeRelease = true
	case <-time.After(time.Second):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Errorf("closing its token canceled an already-admitted commit: %v", err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("snapshot cleanup did not finish after commit release")
	}
	if !closedBeforeRelease {
		t.Error("snapshot cleanup waited for a callback while the Store mutex was held")
	}
	if err := store.TryCompare(context.Background(), snapshot, func() error { return nil }); !errors.Is(err, ErrSnapshotConflict) {
		t.Fatalf("closed token admitted a subsequent commit: %v", err)
	}
}

func TestSettingsSnapshotConcurrentCloseReclaimsLastReference(t *testing.T) {
	store := newRevisionStore(t)
	first := revisionSnapshot(t, store, "sources", "grants")
	second := revisionSnapshot(t, store, "sources", "grants")
	oldIdentity := first.entries["sources"].identity
	var closing sync.WaitGroup
	closing.Add(2)
	for attempt := 0; attempt < 2; attempt++ {
		go func() { defer closing.Done(); first.Close() }()
	}
	closing.Wait()
	store.revisionMu.Lock()
	readers := store.revisions["sources"].readers
	store.revisionMu.Unlock()
	if readers != 1 {
		t.Fatalf("concurrent duplicate Close decremented another token: %d", readers)
	}
	if err := store.TryCompare(context.Background(), second, func() error { return nil }); err != nil {
		t.Fatalf("another live token was released: %v", err)
	}
	second.Close()
	store.revisionMu.Lock()
	retained := len(store.revisions)
	store.revisionMu.Unlock()
	if retained != 0 {
		t.Fatal("last reference retained revision tombstones")
	}
	recreated := revisionSnapshot(t, store, "sources", "grants")
	if recreated.entries["sources"].identity == oldIdentity {
		t.Fatal("recreated metadata reused an identity retained by an old token")
	}
}

func TestSettingsSnapshotValuesRemainOwned(t *testing.T) {
	store := newRevisionStore(t)
	before := revisionSnapshot(t, store, "sources", "grants")
	input := map[string]any{"label": "accepted"}
	updated, err := store.CompareUpdate(context.Background(), before, "sources", func(any, bool) (any, error) { return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Close()
	input["label"] = "changed outside the store"
	value, found, err := updated.Lookup("sources")
	if err != nil || !found || value.Value.(map[string]any)["label"] != "accepted" {
		t.Fatalf("writer retained mutable snapshot values: %+v %v", value, err)
	}
	value.Value.(map[string]any)["label"] = "changed through Lookup"
	value, found, err = updated.Lookup("sources")
	if err != nil || !found || value.Value.(map[string]any)["label"] != "accepted" {
		t.Fatalf("reader retained mutable snapshot values: %+v %v", value, err)
	}
}
