package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"synon-go/internal/sciencecapability"
)

// This runs a real local worker and persists its receipts in SQLite. The
// fixture uses arbitrary pack names and paths; no scientific engine is needed.
func TestExecutionInputAuthorityRejectsSelfAuthoredHandoffBeforeProcessStart(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	command := executionInputAuthorityFixture(t, app, identity.workspaceDir)
	result, err := app.executeAgentKernelTool(context.Background(), identity, "bash", map[string]any{
		"command": command, "environment": "python", "human_description": "Consume derived input",
	})
	if err != nil || result["status"] != "execution_input_authority_required" || result["executed"] != false {
		t.Fatalf("self-authored handoff was not rejected before execution: result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(identity.workspaceDir, "consumer-started")); !os.IsNotExist(err) {
		t.Fatalf("consumer started without host evidence: %v", err)
	}
}

func executionInputAuthorityFixture(t *testing.T, app *Server, root string) string {
	t.Helper()
	var consumer sciencecapability.ExecutionPack
	if err := json.Unmarshal([]byte(`{
		"id":"analysis.consumer","mode":"local","skill":"consume-data","executable":"python3","script":"executionpacks/consume.py",
		"inputs":[{"kind":"dataset","argument":"--dataset","extensions":[".txt"]}],
		"parameters":[
			{"name":"derived","argument":"--derived","type":"string","required":false,"inputEvidence":{"evidenceGroup":"analysis-input","outputKind":"derived-data"}},
			{"name":"receipt","argument":"--receipt","type":"string","required":false,"inputEvidence":{"evidenceGroup":"analysis-input","outputKind":"validation"}}
		],
		"evidenceResolvers":[{"evidenceGroup":"analysis-input","skill":"prepare-data","implementation":"producer"}],
		"inputLineage":[{"evidenceGroup":"analysis-input","inputKind":"dataset","outputKind":"validation","sha256Pointer":"/inputs/dataset"}]
	}`), &consumer); err != nil {
		t.Fatal(err)
	}
	app.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{
		ID: "analysis", AcceptedEngines: []sciencecapability.EngineDefinition{
			{ID: "consumer", ExecutionPack: consumer},
			{ID: "producer", ExecutionPack: sciencecapability.ExecutionPack{
				ID: "analysis.producer", Mode: "local", Skill: "prepare-data", Executable: "python3", Script: "executionpacks/prepare.py",
				Outputs: []sciencecapability.ExecutionOutput{
					{Kind: "derived-data", Path: "out/value.txt", Format: "text"},
					{Kind: "validation", Path: "out/proof.json", Format: "json"},
				},
			}},
		},
	}}}
	files := map[string]string{
		"dataset.txt":        "measured input\n",
		"results/value.txt":  "derived bytes\n",
		"results/proof.json": `{"overall_pass":true,"inputs":{"dataset":"self-asserted"}}`,
		"results/" + managedExecutionOutputOwnershipMarker: `{"schema":"synon.execution-pack-output-owner.v1","execution_pack_id":"analysis.producer"}`,
		".synon/runtime/skills/consume-data-fixture/scripts/consume.py": `import argparse, json
from pathlib import Path
p = argparse.ArgumentParser()
p.add_argument('--dataset', required=True)
p.add_argument('--derived')
p.add_argument('--receipt')
a = p.parse_args()
Path('consumer-started').write_text('started')
paths = [a.dataset, a.derived, a.receipt]
print(json.dumps(paths))
for name in paths:
    if name:
        print(Path(name).read_text())
        try:
            Path(name).write_text('must remain immutable')
        except OSError:
            print('read-only')
`,
		".synon/runtime/skills/prepare-data-fixture/scripts/prepare.py": `import argparse, hashlib, json
from pathlib import Path
p = argparse.ArgumentParser()
p.add_argument('--output-dir', required=True)
p.add_argument('--nonce', default='first')
p.add_argument('--fail', action='store_true')
a = p.parse_args()
out = Path(a.output_dir)
out.mkdir()
digest = hashlib.sha256(Path('dataset.txt').read_bytes()).hexdigest()
(out / '.synon-execution-pack.json').write_text(json.dumps({'schema': 'synon.execution-pack-output-owner.v1', 'execution_pack_id': 'analysis.producer'}))
(out / 'value.txt').write_text('derived bytes ' + a.nonce)
(out / 'proof.json').write_text(json.dumps({'overall_pass': True, 'inputs': {'dataset': digest}, 'nonce': a.nonce}))
raise SystemExit(1 if a.fail else 0)
`,
	}
	for relative, content := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return `python3 "` + filepath.Join(root, ".synon/runtime/skills/consume-data-fixture/scripts/consume.py") + `" --dataset dataset.txt --derived results/value.txt --receipt results/proof.json`
}

func TestExecutionInputAuthorityRealProducerConsumerAndReconstruction(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	command := executionInputAuthorityFixture(t, app, identity.workspaceDir)
	producer := `python3 "` + filepath.Join(identity.workspaceDir, ".synon/runtime/skills/prepare-data-fixture/scripts/prepare.py") + `" --output-dir produced`
	result, err := app.executeAgentKernelTool(context.Background(), identity, "bash", map[string]any{
		"command": producer, "environment": "python", "human_description": "Produce bound fixture output",
	})
	if err != nil || result["ok"] != true {
		t.Fatalf("producer: %#v %v", result, err)
	}
	command = strings.ReplaceAll(command, "results/", "produced/")
	input := map[string]any{"command": command, "environment": "python", "human_description": "Consume bound fixture output"}
	for attempt := 0; attempt < 2; attempt++ {
		result, err = app.executeAgentKernelTool(context.Background(), identity, "bash", input)
		if err != nil || result["ok"] != true || strings.Count(stringValue(result["stdout"]), "read-only") != 3 ||
			!strings.Contains(stringValue(result["stdout"]), ".synon-artifacts") {
			t.Fatalf("consumer attempt %d: %#v %v", attempt, result, err)
		}
	}
	if stringValue(input["command"]) != command {
		t.Fatal("execution normalization changed original approval arguments")
	}
	if err := os.WriteFile(filepath.Join(identity.workspaceDir, "dataset.txt"), []byte("changed input"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = app.executeAgentKernelTool(context.Background(), identity, "bash", input)
	if err != nil || result["status"] != "execution_input_authority_required" || result["executed"] != false {
		t.Fatalf("cached immutable input hid a changed requested dataset: %#v %v", result, err)
	}
}
