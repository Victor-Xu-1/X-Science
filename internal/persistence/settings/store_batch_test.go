package settings

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateBatchPublishesRelatedKeysWithOneSnapshot(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := store.UpdateBatch(map[string]func(any, bool) (any, error){
		"skills.sources": func(any, bool) (any, error) { return []string{"source-a"}, nil },
		"skills.history": func(any, bool) (any, error) { return []string{"import-a"}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	reopened := New(store.path)
	source, sourceFound, err := reopened.Get("skills.sources")
	if err != nil || !sourceFound {
		t.Fatalf("source=%#v found=%t err=%v", source, sourceFound, err)
	}
	history, historyFound, err := reopened.Get("skills.history")
	if err != nil || !historyFound || !history.UpdatedAt.Equal(source.UpdatedAt) {
		t.Fatalf("history=%#v found=%t err=%v", history, historyFound, err)
	}
}

func TestUpdateBatchCallbackFailureLeavesEveryKeyUnchanged(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	if _, err := store.Set("first", "before"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("reject second transformation")
	err = store.UpdateBatch(map[string]func(any, bool) (any, error){
		"first":  func(any, bool) (any, error) { return "after", nil },
		"second": func(any, bool) (any, error) { return nil, injected },
	})
	if !errors.Is(err, injected) {
		t.Fatalf("callback failure=%v", err)
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("batch callback failure changed file: %v", err)
	}
}

func TestUpdateBatchSaveFailureLeavesEveryKeyUnchanged(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	if _, err := store.Set("first", "before"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	err = store.UpdateBatch(map[string]func(any, bool) (any, error){
		"first":  func(any, bool) (any, error) { return "after", nil },
		"second": func(any, bool) (any, error) { return "new value", nil },
	})
	if err == nil {
		t.Fatal("batch save failure was hidden")
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("batch save failure changed file: %v", err)
	}
}
