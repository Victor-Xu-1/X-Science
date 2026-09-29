package tasks

import (
	"path/filepath"
	"testing"
)

func TestFailedTaskStatusPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	store := New(path)
	task, err := store.CreateWithOptions(CreateOptions{Subject: "command", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	failed := "failed"
	if _, _, _, err := store.UpdateWithOptions(task.ID, UpdateOptions{Status: &failed}); err != nil {
		t.Fatal(err)
	}
	read, found, err := New(path).Get(task.ID)
	if err != nil || !found || read.Status != failed {
		t.Fatalf("failed task was not durable: %+v found=%t err=%v", read, found, err)
	}
	invalid := "unknown-terminal"
	if _, _, _, err := store.UpdateWithOptions(task.ID, UpdateOptions{Status: &invalid}); err == nil {
		t.Fatal("unsupported status was accepted")
	}
}
