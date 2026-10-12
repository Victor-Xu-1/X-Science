package config

import (
	"os"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestPublishedDefaultHomeIsRetainedWithoutPointerRewrite(t *testing.T) {
	parent := t.TempDir()
	canonical := filepath.Join(parent, ".x-science")
	published := filepath.Join(parent, ".synon-go")
	path := filepath.Join(published, "workspace", workspace.PublishedDatabaseFilename)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected, retained, err := retainedPublishedDefaultHome(canonical)
	if err != nil || !retained || selected != published {
		t.Fatalf("published authority: %q %t %v", selected, retained, err)
	}
	if _, err := os.Stat(canonical); !os.IsNotExist(err) {
		t.Fatal("inspection created a competing canonical home")
	}
	newPath := filepath.Join(canonical, workspace.DatabaseRelativePath)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("independent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := retainedPublishedDefaultHome(canonical); err == nil {
		t.Fatal("ambiguous default authorities were guessed")
	}
}
