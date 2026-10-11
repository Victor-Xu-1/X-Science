package workspace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectPreparationFollowsRowAndOutboxValidation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, scenario := range []struct {
		name, projectName, eventID string
	}{
		{name: "row", projectName: ""},
		{name: "outbox", projectName: "Valid", eventID: strings.Repeat("x", outboxMaxIdempotencyBytes+1)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			called := false
			_, err := store.CreateProjectRealtimeWithPreparation(context.Background(), CreateProjectInput{
				ID: "invalid-" + scenario.name, Name: scenario.projectName, Path: "/owned",
			}, scenario.eventID, func(Project) (PreparedProjectResource, error) {
				called = true
				return PreparedProjectResource{}, nil
			})
			if err == nil || called {
				t.Fatalf("preparation before validation: called=%t err=%v", called, err)
			}
			if _, found, err := store.GetProject("invalid-" + scenario.name); err != nil || found {
				t.Fatalf("invalid mutation committed: found=%t err=%v", found, err)
			}
		})
	}
	if pending, err := store.CountUndeliveredRealtimeOutbox(context.Background()); err != nil || pending != 0 {
		t.Fatalf("invalid mutation emitted outbox: pending=%d err=%v", pending, err)
	}
}

func TestProjectPreparationRetainsDurablyReferencedPath(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.CreateProject(CreateProjectInput{ID: "referenced", Name: "Referenced", Path: "/retained"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.compensateProjectPreparation(project, func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("referenced preparation removed: called=%t err=%v", called, err)
	}
}

func TestProjectPreparationRetainsUnknownPersistenceOutcome(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.compensateProjectPreparation(Project{ID: "unknown", Path: "/retained"}, func() error {
		called = true
		return errors.New("must not run")
	})
	if err == nil || called {
		t.Fatalf("unknown persistence preparation removed: called=%t err=%v", called, err)
	}
}

func TestProjectPreparationRetainsForeignDurablePathReference(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "legacy-reference", Name: "Legacy", Path: "/retained"}); err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.compensateProjectPreparation(Project{ID: "failed-new-project", Path: "/retained"}, func() error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("foreign referenced preparation removed: called=%t err=%v", called, err)
	}
}

func TestProjectPreparationCompensationHoldsWriterAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := store.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan error, 1)
	err = store.compensateProjectPreparation(Project{ID: "aborted", Path: "/prepared"}, func() error {
		go func() {
			finished <- other.WithTransaction(context.Background(), func(tx *sql.Tx) error {
				close(started)
				_, err := other.CreateProjectOutboxTx(context.Background(), tx, CreateProjectInput{
					ID: "concurrent", UserID: "local", Name: "Concurrent", Path: "/prepared",
				})
				return err
			})
		}()
		<-started
		select {
		case err := <-finished:
			t.Errorf("concurrent writer admitted during compensation: %v", err)
			finished <- err
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("writer not released after compensation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer remained blocked after compensation")
	}
}
