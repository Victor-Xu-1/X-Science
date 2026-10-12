package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrantedFileRejectsOutsideScopeBeforeExistenceProbe(t *testing.T) {
	granted, outside := t.TempDir(), t.TempDir()
	existing := filepath.Join(outside, "existing.txt")
	if err := os.WriteFile(existing, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var previous string
	for _, path := range []string{existing, filepath.Join(outside, "missing.txt")} {
		file, err := openGrantedRegularFile(path, []hostGrant{{Path: granted, Mode: "read"}})
		if err == nil || file != nil {
			if file != nil {
				_ = file.Close()
			}
			t.Fatal("ungranted file admitted")
		}
		if previous != "" && previous != err.Error() {
			t.Fatalf("outside file existence leaked: %q versus %q", previous, err)
		}
		previous = err.Error()
	}
}
