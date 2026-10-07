package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderDurableStageRetainsPartialDataAndRejectsPathDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "output.partial")
	if err := os.WriteFile(partial, []byte("verified prefix"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, release, err := prepareProviderOperationStage(root)
	if err != nil || stage != root {
		t.Fatalf("stage=%q err=%v", stage, err)
	}
	release()
	if _, err := os.Stat(partial); err != nil {
		t.Fatal("durable partial was removed", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareProviderOperationStage(alias); err == nil {
		t.Fatal("stage symlink accepted")
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareProviderOperationStage(root); err == nil {
		t.Fatal("non-private stage accepted")
	}
}

func TestProviderDurableStageDoesNotFollowOrReuseStaleMetadata(t *testing.T) {
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "reply.json")); err != nil {
		t.Fatal(err)
	}
	if err := stageProviderOperationRequest(root, []byte(`{}`)); err == nil {
		t.Fatal("reply symlink accepted")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "unchanged" {
		t.Fatal("metadata target changed")
	}
	if err := os.Remove(filepath.Join(root, "reply.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reply.json"), []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageProviderOperationRequest(root, []byte(`{"new":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "reply.json")); !os.IsNotExist(err) {
		t.Fatal("stale reply retained")
	}
}
