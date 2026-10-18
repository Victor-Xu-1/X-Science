package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePublicationConfirmsDirectoryOutsideCommitAndKeepsUncertainBytes(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stage, err := stageWorkspaceFile(context.Background(), root, strings.NewReader("fixture"), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.close()
	inside, synced := false, 0
	fault := errors.New("controlled directory sync failure")
	stage.directorySyncer = func(directory *os.File) error {
		if inside {
			t.Fatal("potentially blocking directory sync held the publication/grant guard")
		}
		info, err := directory.Stat()
		if err != nil || !info.IsDir() {
			t.Fatalf("sync did not receive an actual open directory: %v", err)
		}
		synced++
		return fault
	}
	guard := func(action func() error) error {
		inside = true
		defer func() { inside = false }()
		return action()
	}
	err = stage.publishWithCommit(context.Background(), "result.txt", guard)
	var uncertainty *workspaceFileDurabilityError
	if !errors.As(err, &uncertainty) || !errors.Is(err, fault) || !uncertainty.published || synced != 1 {
		t.Fatalf("unconfirmed publication claimed durable success: synced=%d err=%v", synced, err)
	}
	if got, err := os.ReadFile(filepath.Join(path, "result.txt")); err != nil || string(got) != "fixture" {
		t.Fatalf("uncertain publication discarded its recovery bytes: %q %v", got, err)
	}
	stage.directorySyncer = func(directory *os.File) error {
		if inside {
			t.Fatal("idempotent verification synchronized while holding the guard")
		}
		synced++
		return directory.Sync()
	}
	if err := stage.publishWithCommit(context.Background(), "result.txt", guard); err != nil || synced != 2 {
		t.Fatalf("same-content retry did not actually confirm durability: synced=%d err=%v", synced, err)
	}
}

func TestWorkspacePublicationPostSyncReplacementCannotBecomeAccepted(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stage, err := stageWorkspaceFile(context.Background(), root, strings.NewReader("fixture"), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.close()
	stage.directorySyncer = func(directory *os.File) error {
		if err := directory.Sync(); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(path, "result.txt"), filepath.Join(path, "original.txt")); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "result.txt"), []byte("changed"), 0600)
	}
	if err := stage.publish(context.Background(), "result.txt"); !errors.Is(err, errWorkspaceFileConflict) {
		t.Fatalf("post-sync replacement gained the old publication's authority: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(path, "result.txt"))
	if err != nil || string(got) != "changed" {
		t.Fatalf("unrelated replacement was overwritten/removed: %q %v", got, err)
	}
}
