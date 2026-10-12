package datadir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNamespaceRecoveryPublicationFailureRetainsOriginalAndResumes(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "original.json")
	raw := []byte(`{"schemaVersion":1,"current":"unchanged user data"}`)
	if err := os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := namespaceFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "recovery.json")
	err = publishNamespaceRecovery(target, snapshot, func(file *os.File, raw []byte) error {
		if _, err := file.Write(raw[:8]); err != nil {
			return err
		}
		return errors.New("interrupted private recovery write")
	})
	if err == nil {
		t.Fatal("partial recovery write was published")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("failed write occupied final recovery identity")
	}
	if err := snapshot.unchanged(source); err != nil {
		t.Fatal("failed recovery write changed original")
	}
	orphan := filepath.Join(directory, ".namespace-recovery-staging-orphan")
	if err := os.WriteFile(orphan, []byte("other interrupted invocation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := namespaceRecovery(target, snapshot); err != nil {
		t.Fatalf("same verified source did not resume: %v", err)
	}
	if current, err := namespaceFile(target); err != nil || current.hash != snapshot.hash {
		t.Fatal("published recovery differs from original")
	}
	if _, err := os.Lstat(orphan); err != nil {
		t.Fatal("unrelated interrupted staging was removed")
	}
	if err := os.WriteFile(target, []byte("foreign recovery identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := namespaceRecovery(target, snapshot); err == nil {
		t.Fatal("foreign final recovery identity was overwritten")
	}
}
