package v11

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReportAcceptsPublishedLegacySchemaName(t *testing.T) {
	root := t.TempDir()
	report := Report{
		Schema: LegacySchemaName, Status: StatusCompleted, MigrationID: "migration-1",
		TargetHome: root, TargetDatabase: WorkspaceDatabaseRelativePath,
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReport(root)
	if err != nil || loaded.Schema != LegacySchemaName {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}
