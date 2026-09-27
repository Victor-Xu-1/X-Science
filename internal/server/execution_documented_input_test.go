package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/sciencecapability"
	"synon-go/internal/skills"
)

func documentedInputTestPack(t *testing.T) sciencecapability.ExecutionPack {
	t.Helper()
	var pack sciencecapability.ExecutionPack
	if err := json.Unmarshal([]byte(`{
		"id":"analysis.primary","mode":"local","skill":"primary","executable":"python3","script":"executionpacks/run.py",
		"inputs":[{"kind":"dataset","argument":"--dataset","extensions":[".txt"]}],
		"parameters":[
			{"name":"point","argument":"--point","type":"number","evidence":"resolved-user-input","evidenceGroup":"control-point","evidenceTerms":["control point"]},
			{"name":"evidence","argument":"--evidence","type":"string"}
		],
		"documentedInputs":[{"evidenceGroup":"control-point","argument":"--evidence","inputKind":"dataset"}],
		"evidenceResolvers":[{"evidenceGroup":"control-point","skill":"resolver","implementation":"Resolver"}]
	}`), &pack); err != nil {
		t.Fatal(err)
	}
	return pack
}

func TestDocumentedInputRealKernelExecutionAndImmutableProvenance(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	pack := documentedInputTestPack(t)
	app.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{ID: "analysis", AcceptedEngines: []sciencecapability.EngineDefinition{{ID: "Primary", ExecutionPack: pack}}}}}
	root := identity.workspaceDir
	script := filepath.Join(root, ".synon/runtime/skills/primary-fixture/scripts/run.py")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	worker := "import argparse,json\nfrom pathlib import Path\np=argparse.ArgumentParser()\np.add_argument('--dataset')\np.add_argument('--point')\np.add_argument('--evidence')\na=p.parse_args()\nfor name in (a.dataset,a.evidence):\n print(name)\n print(Path(name).read_text())\n try: Path(name).write_text('must not change')\n except OSError: print('read-only')\n"
	if err := os.WriteFile(script, []byte(worker), 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("measured observations\n")
	if err := os.WriteFile(filepath.Join(root, "source.txt"), source, 0600); err != nil {
		t.Fatal(err)
	}
	record := executionDocumentedInput{Schema: "synon.documented-input.v1", EvidenceGroup: "control-point", InputSHA256: fmt.Sprintf("%x", sha256.Sum256(source)), Basis: "structure-derived", Method: "Derive a control parameter from the supplied observations.", Sources: []string{"source.txt: measured observations"}, Limitations: "Exploratory input, not a successful resolver result.", Values: map[string]any{"point": 4.5}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "rationale.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := "python3 " + shellSingleQuote(script) + " --dataset source.txt --point 4.5 --evidence rationale.json"
	input := map[string]any{"command": command, "environment": "python", "human_description": "Execute documented alternative"}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := app.executeAgentKernelTool(context.Background(), identity, "bash", input)
		if err != nil || result["ok"] != true || strings.Count(stringValue(result["stdout"]), "read-only") != 2 || !strings.Contains(stringValue(result["stdout"]), ".synon-artifacts") {
			t.Fatalf("real documented execution %d failed: %#v %v", attempt, result, err)
		}
	}
	if stringValue(input["command"]) != command {
		t.Fatal("original approval arguments mutated")
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("changed observations"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := app.executeAgentKernelTool(context.Background(), identity, "bash", input)
	if err != nil || result["status"] != "documented_input_required" || result["executed"] != false {
		t.Fatalf("stale provenance executed: %#v %v", result, err)
	}
}

func TestDocumentedInputRecordDoesNotManufactureAutomaticEvidence(t *testing.T) {
	pack := documentedInputTestPack(t)
	route, found := pack.DocumentedInput("control-point")
	if !found {
		t.Fatal("documented input missing")
	}
	valid := executionDocumentedInput{Schema: "synon.documented-input.v1", EvidenceGroup: "control-point", Basis: "exploratory", Method: "Observed-data derivation", Sources: []string{"source observation"}, Limitations: "Not an established result", Values: map[string]any{"point": 4.5}}
	values := map[string]string{"--point": "4.5"}
	for name, mutate := range map[string]func(*executionDocumentedInput){
		"wrong group":       func(r *executionDocumentedInput) { r.EvidenceGroup = "other" },
		"claims prediction": func(r *executionDocumentedInput) { r.Basis = "successful-prediction" },
		"missing sources":   func(r *executionDocumentedInput) { r.Sources = nil },
		"missing method":    func(r *executionDocumentedInput) { r.Method = "" },
		"missing limits":    func(r *executionDocumentedInput) { r.Limitations = "" },
		"different value":   func(r *executionDocumentedInput) { r.Values = map[string]any{"point": 5.0} },
		"extra value":       func(r *executionDocumentedInput) { r.Values = map[string]any{"point": 4.5, "other": 1.0} },
	} {
		t.Run(name, func(t *testing.T) {
			record := valid
			mutate(&record)
			if validateDocumentedInputRecord(record, pack, route, values) == nil {
				t.Fatal("invalid provenance accepted")
			}
		})
	}
	if err := validateDocumentedInputRecord(valid, pack, route, values); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentedInputAlternativeDoesNotRequireUserToRepeatDerivedValues(t *testing.T) {
	pack := documentedInputTestPack(t)
	selected := []sciencecapability.ExecutionEvidenceResolver{{EvidenceGroup: "control-point", Skill: "resolver", Implementation: "Resolver"}}
	if blocked := managedExecutionPackParameterEvidencePreflight(pack, "python3 run.py --dataset source.txt --point 4.5 --evidence rationale.json", nil, "en", selected); blocked != nil {
		t.Fatalf("documented alternative incorrectly requires resolver success or user-supplied tuple: %#v", blocked)
	}
	if blocked := managedExecutionPackParameterEvidencePreflight(pack, "python3 run.py --dataset source.txt --point 4.5", nil, "en", selected); blocked == nil {
		t.Fatal("undocumented values gained input authority")
	}
}

func TestDocumentedInputAlternativeAllowsPreparationWithoutResolverMonopoly(t *testing.T) {
	pack := documentedInputTestPack(t)
	catalog := skills.NewCatalog()
	catalog.AddSkill(skills.Skill{Name: "primary", ImplementationIdentities: []string{"Primary"}})
	run := &sessionRunnerChatRun{SelectedImplementations: []string{"Primary"}}
	gateway := serverAgentRuntimeToolGateway{server: &Server{skillCatalog: catalog, scienceCapabilities: &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{ID: "analysis", AcceptedEngines: []sciencecapability.EngineDefinition{{ID: "Primary", ExecutionPack: pack}}}}}}, taskRun: run}
	if blocked := gateway.agentRuntimeControlledEvidenceDerivationPreflight("python", map[string]any{"code": "print('derive control point from supplied observations')"}); blocked != nil {
		t.Fatalf("documented preparation was blocked by a resolver-only gate: %#v", blocked)
	}
}
