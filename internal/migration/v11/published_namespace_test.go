package v11

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedV11CredentialTableRemainsAnExplicitSourceDisposition(t *testing.T) {
	planned, err := planTableDispositions(map[string]int64{"synon_llm_api_keys": 1})
	if err != nil || planned["synon_llm_api_keys"].Action != DispositionArchive {
		t.Fatalf("published encrypted source table lost its disposition: %v %v", planned, err)
	}
}

func TestLoadReportRetainsCompletedPublishedSchemaAndTargetChecks(t *testing.T) {
	for _, schema := range []string{SchemaName, "synonbiomed-v1.1", "unknown-v1.1"} {
		t.Run(schema, func(t *testing.T) {
			root := t.TempDir()
			report := Report{Schema: schema, Status: StatusCompleted, MigrationID: "retained", TargetHome: root}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ManifestFilename), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadReport(root)
			if schema == "unknown-v1.1" {
				if err == nil {
					t.Fatal("unknown report schema was accepted")
				}
				return
			}
			if err != nil || loaded.Schema != schema {
				t.Fatalf("historical report changed or rejected: %#v %v", loaded, err)
			}
			report.TargetHome = filepath.Join(root, "foreign")
			raw, _ = json.Marshal(report)
			if err := os.WriteFile(filepath.Join(root, ManifestFilename), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadReport(root); err == nil {
				t.Fatal("foreign report target was accepted")
			}
		})
	}
}
