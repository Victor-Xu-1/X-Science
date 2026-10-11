package workspace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestProjectIdentityRetirementSurvivesDeleteAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "retired", UserID: "alice", Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject("retired"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, owner := range []string{"alice", "bob"} {
		if _, err := store.CreateProject(CreateProjectInput{ID: "retired", UserID: owner, Name: "Replacement"}); err == nil {
			t.Fatalf("deleted project identity was reused by %s", owner)
		}
	}
	if _, found, err := store.GetProject("retired"); err != nil || found {
		t.Fatalf("retired project reappeared: found=%v err=%v", found, err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "fresh", UserID: "bob", Name: "New identity"}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectIdentityRetirementSerializesConcurrentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, store := range []*Store{first, second} {
		wait.Add(1)
		go func(index int, store *Store) {
			defer wait.Done()
			<-start
			_, err := store.CreateProject(CreateProjectInput{ID: "contended", UserID: []string{"alice", "bob"}[index], Name: "Concurrent"})
			results <- err
		}(index, store)
	}
	close(start)
	wait.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same-ID concurrent winners=%d", success)
	}
	if err := first.DeleteProject("contended"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.CreateProject(CreateProjectInput{ID: "contended", UserID: "charlie", Name: "Later reuse"}); err == nil {
		t.Fatal("other connection reused retired ID")
	}
}

func TestProjectIdentityRetirementCoversSQLConflictModesAndUpdates(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "used", UserID: "alice", Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"IGNORE", "REPLACE"} {
		if _, err := store.db.Exec("INSERT OR " + mode + " INTO projects(id,user_id,name,path,created_at,updated_at) VALUES('used','bob','Replacement','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err == nil {
			t.Fatalf("SQL conflict mode %s bypassed project identity ownership", mode)
		}
	}
	if _, err := store.db.Exec("UPDATE projects SET id='renamed' WHERE id='used'"); err == nil {
		t.Fatal("project identity was renamed")
	}
	if err := store.DeleteProject("used"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"IGNORE", "REPLACE"} {
		if _, err := store.db.Exec("INSERT OR " + mode + " INTO projects(id,user_id,name,path,created_at,updated_at) VALUES('used','bob','Replacement','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err == nil {
			t.Fatalf("SQL conflict mode %s resurrected deleted identity", mode)
		}
	}
}

func TestProjectIdentityRetirementRollbacksLeaveIDRetryable(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectOutboxTx(ctx, tx, CreateProjectInput{ID: "rolled-back", UserID: "alice", Name: "Uncommitted"}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "rolled-back", UserID: "bob", Name: "Retry"}); err != nil {
		t.Fatalf("rolled-back ID was burned: %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO projects(id,user_id,name,path,created_at,updated_at) VALUES('invalid','alice',NULL,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err == nil {
		t.Fatal("invalid statement unexpectedly succeeded")
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "invalid", UserID: "bob", Name: "Retry"}); err != nil {
		t.Fatalf("failed statement burned ID: %v", err)
	}
	preparationFailure := errors.New("controlled preparation failure")
	if _, err := store.CreateProjectRealtimeWithPreparation(ctx, CreateProjectInput{ID: "prepared", UserID: "alice", Name: "Uncommitted"}, "", func(Project) (PreparedProjectResource, error) { return PreparedProjectResource{}, preparationFailure }); !errors.Is(err, preparationFailure) {
		t.Fatalf("preparation result=%v", err)
	}
	if _, err := store.CreateProjectRealtime(ctx, CreateProjectInput{ID: "prepared", UserID: "bob", Name: "Retry"}, ""); err != nil {
		t.Fatalf("failed preparation burned ID: %v", err)
	}
}

func TestProjectIdentityRetirementRejectsOutboxReuseBeforePreparation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.CreateProjectRealtime(ctx, CreateProjectInput{ID: "outbox-used", UserID: "alice", Name: "Original"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTransaction(ctx, func(tx *sql.Tx) error {
		_, err := store.DeleteProjectOutboxTx(ctx, tx, "outbox-used", "alice")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = store.CreateProjectRealtimeWithPreparation(ctx, CreateProjectInput{ID: "outbox-used", UserID: "bob", Name: "Replacement"}, "", func(Project) (PreparedProjectResource, error) { called = true; return PreparedProjectResource{}, nil })
	if err == nil || called {
		t.Fatalf("retired ID reached preparation: called=%v err=%v", called, err)
	}
}
