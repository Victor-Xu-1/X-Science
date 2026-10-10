package sciencecapability

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
)

func TestDefaultCatalogLocksCapabilityAndEngineIdentities(t *testing.T) {
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id      string
		engines []string
	}{
		{"molecule-generation", []string{"genmol", "molmim"}},
		{"binding-pocket-prediction", []string{"p2rank"}},
		{"molecular-docking", []string{"diffdock", "autodock-vina", "gnina"}},
		{"binding-affinity", []string{"boltz-affinity", "gnina", "mmgbsa"}},
		{"document-workbench", []string{"python-document-workbench"}},
		{"pdf-exploration", []string{"pypdfium"}},
		{"single-cell-rna-analysis", []string{"scanpy-harmony"}},
		{"instrument-data-allotrope", []string{"allotropy"}},
		{"nextflow-development", []string{"nextflow"}},
	}
	if len(catalog.Capabilities) != len(want) {
		t.Fatalf("capabilities=%d want=%d", len(catalog.Capabilities), len(want))
	}
	seen := map[string]bool{}
	for index, definition := range catalog.Capabilities {
		expected := want[index]
		if definition.ID != expected.id {
			t.Fatalf("capability[%d]=%q want=%q", index, definition.ID, expected.id)
		}
		if seen[definition.ID] {
			t.Fatalf("duplicate capability %q", definition.ID)
		}
		seen[definition.ID] = true
		if definition.Description == "" || len(definition.AcceptedEngines) == 0 {
			t.Fatalf("capability %q lost description or engines", definition.ID)
		}
		gotEngines := make([]string, 0, len(definition.AcceptedEngines))
		for _, engine := range definition.AcceptedEngines {
			gotEngines = append(gotEngines, engine.ID)
			if engine.ID == "" || len(engine.RequiredInputs) == 0 || len(engine.ScoreKinds) == 0 || len(engine.RequiredArtifacts) == 0 {
				t.Fatalf("engine %s/%s lost closed contract fields: %#v", definition.ID, engine.ID, engine)
			}
			if engine.ExecutionPack.ID != definition.ID+"."+engine.ID {
				t.Fatalf("execution pack id=%q want=%q", engine.ExecutionPack.ID, definition.ID+"."+engine.ID)
			}
			if engine.ExecutionPack.Skill == "" || engine.ExecutionPack.Mode == "" {
				t.Fatalf("execution pack %q lost skill or mode", engine.ExecutionPack.ID)
			}
		}
		if !reflect.DeepEqual(gotEngines, expected.engines) {
			t.Fatalf("capability %q engines=%v want=%v", definition.ID, gotEngines, expected.engines)
		}
	}
}

func TestDefaultCatalogSHA256MatchesBundledContract(t *testing.T) {
	raw, err := bundledExecutionPacks.ReadFile("scientific-capabilities.v2.json")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(raw)
	got, err := DefaultCatalogSHA256()
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("DefaultCatalogSHA256=%q want=%q", got, hex.EncodeToString(want[:]))
	}
}

func TestEvaluateDefaultCatalogWithoutWitnessesIsMissingNotSatisfied(t *testing.T) {
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	required := make([]string, 0, len(catalog.Capabilities))
	for _, definition := range catalog.Capabilities {
		required = append(required, definition.ID)
	}
	required = uniqueSorted(required)
	result := Evaluate(catalog, required, nil)
	if len(result.Satisfied) != 0 || len(result.Invalid) != 0 {
		t.Fatalf("empty-witness evaluation leaked success or invalid: %#v", result)
	}
	if !reflect.DeepEqual(result.Missing, required) {
		t.Fatalf("missing=%v want=%v", result.Missing, required)
	}
}
