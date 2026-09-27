package server

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"synon-go/internal/sciencecapability"
)

func TestExecutionInputRecoveryOffersOnlyRegisteredAlternatives(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	root := identity.workspaceDir
	for _, name := range []string{"source.txt", "derived.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("unattested input"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(root, ".synon/runtime/skills/primary-fixture/scripts/run.py")
	input := map[string]any{"command": "python3 " + shellSingleQuote(script) + " --dataset source.txt --derived derived.json", "environment": "python"}
	for _, test := range []struct {
		name, routeGroup string
		wantAlternative  bool
	}{
		{"registered_same_group", "control-point", true},
		{"different_group", "unrelated", false},
		{"no_registered_route", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pack := documentedInputTestPack(t)
			pack.Parameters = append(pack.Parameters, sciencecapability.ExecutionParameter{
				Name: "derived", Argument: "--derived", Type: "string",
				InputEvidence: &sciencecapability.ExecutionInputEvidence{EvidenceGroup: "control-point", OutputKind: "derived-value"},
			})
			if test.routeGroup == "" {
				pack.DocumentedInputs = nil
			} else {
				pack.DocumentedInputs[0].EvidenceGroup = test.routeGroup
			}
			app.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{ID: "analysis", AcceptedEngines: []sciencecapability.EngineDefinition{{ID: "Primary", ExecutionPack: pack}}}}}
			_, blocked, err := app.bindManagedExecutionInputs(context.Background(), identity.access, root, root, "bash", input)
			if err != nil || blocked["status"] != "execution_input_authority_required" || blocked["executed"] != false || blocked["reason"] != "no_matching_execution" {
				t.Fatalf("unattested resolver output must remain nonexecuted: %#v %v", blocked, err)
			}
			alternative := mapValue(blocked["documented_input_alternative"])
			if !test.wantAlternative {
				if len(alternative) != 0 {
					t.Fatalf("invented an unregistered alternative: %#v", blocked)
				}
				return
			}
			if alternative["argument"] != "--evidence" || alternative["evidence_group"] != "control-point" ||
				alternative["schema"] != "synon.documented-input.v1" || alternative["input_kind"] != "dataset" {
				t.Fatalf("registered alternative is hidden by resolver-only recovery: %#v", blocked)
			}
			if !strings.Contains(stringValue(blocked["recovery"]), "documented") {
				t.Fatalf("recovery still demands only automatic resolver output: %#v", blocked)
			}
			if alternative["input_argument"] != "--dataset" ||
				!reflect.DeepEqual(alternative["replaces_arguments"], []string{"--derived"}) ||
				!reflect.DeepEqual(alternative["parameters"], []map[string]string{{"name": "point", "argument": "--point", "type": "number"}}) {
				t.Fatalf("alternative arguments do not match the registered contract: %#v", alternative)
			}
			for _, reason := range []string{"materialization_unavailable", "receipts_unavailable"} {
				boundary := executionInputAuthorityBoundary(pack, "control-point", reason)
				if boundary["retryable"] != true || boundary["documented_input_alternative"] != nil ||
					!strings.Contains(stringValue(boundary["recovery"]), "Preserve the successful upstream execution") {
					t.Fatalf("infrastructure recovery must preserve completed work: %#v", boundary)
				}
			}
		})
	}
}
