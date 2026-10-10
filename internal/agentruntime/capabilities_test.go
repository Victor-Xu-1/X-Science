package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/assets"
)

func TestValidateCapabilityNamesFailsClosed(t *testing.T) {
	cases := []struct {
		name           string
		kind           string
		values         []string
		requireBundled bool
		want           string
	}{
		{"empty skills", "skill", nil, false, "has no skill assignments"},
		{"blank skill", "skill", []string{"  "}, false, "invalid skill assignment"},
		{"duplicate skill", "skill", []string{"docking", "DOCKING"}, false, "duplicate skill assignment"},
		{"empty connectors", "connector", []string{}, true, "has no connector assignments"},
		{"connector missing bundled prefix", "connector", []string{"chembl"}, true, "invalid connector assignment"},
		{"duplicate connector", "connector", []string{"bundled:chembl", "bundled:CHEMBL"}, true, "duplicate connector assignment"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateCapabilityNames("AIDD_EXPERT", testCase.kind, testCase.values, testCase.requireBundled)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want substring %q", err, testCase.want)
			}
		})
	}
}

func TestLoadAgentCapabilitiesRejectsUnsafeAssignments(t *testing.T) {
	valid := `{
  "schemaVersion": 1,
  "agents": [{
    "name": "AIDD_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }]
}`
	cases := map[string]string{
		"unknown field": `{
  "schemaVersion": 1,
  "agents": [{
    "name": "AIDD_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }],
  "fallback": true
}`,
		"operon assignment": `{
  "schemaVersion": 1,
  "agents": [{
    "name": "OPERON",
    "skills": ["alphafold2"],
    "connectors": ["bundled:chembl"]
  }]
}`,
		"unknown agent": `{
  "schemaVersion": 1,
  "agents": [{
    "name": "UNKNOWN_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }]
}`,
		"duplicate agent": `{
  "schemaVersion": 1,
  "agents": [{
    "name": "AIDD_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }, {
    "name": "aidd_expert",
    "skills": ["cheminfo-render"],
    "connectors": ["bundled:pubchem"]
  }]
}`,
		"wrong schema": `{
  "schemaVersion": 2,
  "agents": [{
    "name": "AIDD_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }]
}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadAgentCapabilities(writeCapabilityManifest(t, raw), capabilityTestManifest()); err == nil {
				t.Fatal("unsafe agent capability assignment was accepted")
			}
		})
	}

	assignments, err := loadAgentCapabilities(writeCapabilityManifest(t, valid), capabilityTestManifest())
	if err != nil {
		t.Fatalf("valid capability assignment rejected: %v", err)
	}
	if got := assignments["AIDD_EXPERT"]; got.Name != "AIDD_EXPERT" || len(got.Skills) != 1 || got.Connectors[0] != "bundled:chembl" {
		t.Fatalf("valid assignment = %#v", got)
	}
}

func TestLoadAgentCapabilitiesRequiresManifestEntry(t *testing.T) {
	root := writeCapabilityManifest(t, `{
  "schemaVersion": 1,
  "agents": [{
    "name": "AIDD_EXPERT",
    "skills": ["drug-discovery-pipeline"],
    "connectors": ["bundled:chembl"]
  }]
}`)
	manifest := capabilityTestManifest()
	manifest.Files = nil
	if _, err := loadAgentCapabilities(root, manifest); err == nil || !strings.Contains(err.Error(), "capabilities.json") {
		t.Fatalf("missing manifest entry error = %v", err)
	}
}

func writeCapabilityManifest(t *testing.T, raw string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "capabilities.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write capabilities.json: %v", err)
	}
	return root
}

func capabilityTestManifest() assets.Manifest {
	return assets.Manifest{
		Agents: []string{"AIDD_EXPERT"},
		Files:  []assets.File{{Path: "capabilities.json"}},
	}
}
