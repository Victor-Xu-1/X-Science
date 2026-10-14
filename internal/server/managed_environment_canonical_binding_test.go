package server

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"x-science/internal/agentruntime"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sciencecapability"
	"x-science/internal/skills"
)

func TestCanonicalEnvironmentBindingRepairsLegacyReceiptAcrossReopen(t *testing.T) {
	for _, mode := range []string{"preflight", "create-reuse", "packages-reuse", "packages-preflight"} {
		t.Run(mode, func(t *testing.T) {
			manager := kernelruntime.NewManager(kernelruntime.Config{CondaEnvsPath: filepath.Join(t.TempDir(), "envs")})
			f := newAgentSaveArtifactsFixtureWithKernelManager(t, manager)
			s := f.server
			s.skillCatalog, s.scienceCapabilities = skills.NewCatalog(), &sciencecapability.Catalog{}
			addSelectionRouteEngine(s, "analysis", "engine-a", "Engine A")
			run := &sessionRunnerChatRun{RequiredScientificCapabilities: []string{"analysis"},
				Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			ctx := withTranscriptRunnerChatRun(context.Background(), run)
			tool := manageEnvironmentsToolName
			input := map[string]any{"mode": "preflight", "name": "requested", "language": "python",
				"implementation": "Engine A 1.2.7", "packages": []any{"fixture"},
				"human_description": "Prepare the requested analysis", "resource_requirements": managedEnvironmentTestResources()}
			if mode == "create-reuse" {
				input["mode"] = "create"
			} else if mode == "packages-reuse" || mode == "packages-preflight" {
				tool = managePackagesToolName
				input = map[string]any{"mode": "install", "environment": "existing", "implementation": "Engine A 1.2.7",
					"packages": []any{"fixture"}, "human_description": "Prepare the requested analysis",
					"resource_requirements": managedEnvironmentTestResources()}
				if mode == "packages-preflight" {
					input["mode"] = "preflight"
				}
			}
			old := map[string]any{"status": "completed", "mode": "reuse", "environment": map[string]any{
				"name": "existing", "status": "ready", "generation": "observed-generation"}}
			appendCanonicalBindingCheckpoint(t, f.repo, f.claim, "legacy", tool, input, old)
			if err := s.hydrateSessionRunnerManagedEnvironmentBindings(ctx, run); err != nil {
				t.Fatal(err)
			}
			authority := &recordingManagedEnvironmentAuthority{
				listResult: []kernelruntime.ManagedEnvironment{{Name: "existing", Language: "python", Status: "ready",
					Generation: "observed-generation", Packages: []string{"fixture=1.0=build=conda"}}},
				mutateResult: kernelruntime.ManagedEnvironment{Name: "existing", Language: "python", Status: "ready",
					Generation: "observed-generation", Packages: []string{"fixture=1.0=build=conda"}},
			}
			value, err := s.executeAgentEnvironmentManagementToolWithAuthority(ctx, f.identity,
				agentruntime.ToolCall{ID: "fresh-" + mode}, tool, input, authority)
			if err != nil {
				t.Fatal(err)
			}
			result := mapValue(value)
			binding := mapValue(result["environment_binding"])
			if stringValue(mapValue(binding["input"])["implementation"]) != "Engine A" || stringValue(binding["tool"]) != tool ||
				!reflect.DeepEqual(run.selectedImplementationsSnapshot(), []string{"Engine A"}) || input["implementation"] != "Engine A 1.2.7" {
				t.Fatalf("producer lost canonical/raw identity separation: binding=%#v selected=%v input=%#v", binding, run.selectedImplementationsSnapshot(), input)
			}
			gateway := serverAgentRuntimeToolGateway{taskRun: run}
			if blocked := gateway.agentRuntimeSelectedImplementationEnvironmentPreflight("bash", map[string]any{"environment": "existing"}); blocked == nil {
				t.Fatal("legacy version-bearing receipt was silently reinterpreted before fresh binding")
			}
			// This is the exact live gateway consumer. The producer, not the
			// consumer's current selection, supplies the admitted identity.
			run.bindManagedEnvironmentToolResult(tool, input, value)
			if blocked := gateway.agentRuntimeSelectedImplementationEnvironmentPreflight("bash", map[string]any{"environment": "existing"}); blocked != nil {
				t.Fatalf("fresh canonical preflight/reuse did not repair live legacy binding: %#v", blocked)
			}
			appendCanonicalBindingCheckpoint(t, f.repo, f.claim, "fresh", tool, input, value)
			catalog, capabilities := s.skillCatalog, s.scienceCapabilities
			if err := s.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := workspace.Open(f.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			repo, err := store.TranscriptRepository(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			reopened := &Server{workspaceStore: store, transcriptStore: repo, skillCatalog: catalog, scienceCapabilities: capabilities}
			restored := &sessionRunnerChatRun{RequiredScientificCapabilities: []string{"analysis"},
				Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
			selection, err := reopened.runnerAskUserSelectionEntries(context.Background(), restored)
			if err != nil {
				t.Fatal(err)
			}
			restored.setSelectedImplementations(selectedAskUserImplementationsFromRunnerEntries(selection)...)
			if err := reopened.hydrateSessionRunnerManagedEnvironmentBindings(context.Background(), restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored.selectedImplementationsSnapshot(), []string{"Engine A"}) ||
				(serverAgentRuntimeToolGateway{taskRun: restored}).agentRuntimeSelectedImplementationEnvironmentPreflight("bash", map[string]any{"environment": "existing"}) != nil {
				t.Fatalf("SQLite reopen lost canonical binding or registry authority: selected=%v bindings=%v", restored.selectedImplementationsSnapshot(), restored.managedEnvironmentImplementationsSnapshot("existing"))
			}
			if authority.mutations != 0 {
				t.Fatalf("reuse fixture unexpectedly mutated environment: %d", authority.mutations)
			}
		})
	}
}

func appendCanonicalBindingCheckpoint(t *testing.T, repo *transcriptstore.Repository, claim transcriptstore.RunnerClaim, id, tool string, input map[string]any, result any) {
	t.Helper()
	_, _, _, err := repo.AppendRunnerCheckpoint(context.Background(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claim, ClientMessageID: "binding-" + id, Phase: transcriptstore.RunnerPhaseExecuting,
		PayloadJSON: mustMarshalRawMessage(map[string]any{"status": "completed", "toolPhase": "completed", "lifecyclePhase": "tool", "toolName": tool,
			"toolCallId": fmt.Sprintf("binding-call-%s", id), "toolInput": input, "toolResult": result}),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalBindingStillRejectsOtherOrUnreadyImplementations(t *testing.T) {
	run := &sessionRunnerChatRun{SelectedImplementations: []string{"Engine A"}}
	for _, status := range []string{"staging", "failed", "unknown"} {
		run.bindManagedEnvironmentToolResult(manageEnvironmentsToolName, map[string]any{"implementation": "Engine A"}, map[string]any{
			"environment":         map[string]any{"name": "not-ready", "status": status},
			"environment_binding": managedEnvironmentBindingReceipt(manageEnvironmentsToolName, map[string]any{"implementation": "Engine A"}),
		})
		if got := run.managedEnvironmentImplementationsSnapshot("not-ready"); len(got) != 0 {
			t.Fatalf("unready result bound identity: %v", got)
		}
	}
	run.bindManagedEnvironmentToolResult(manageEnvironmentsToolName, map[string]any{}, map[string]any{
		"environment":         map[string]any{"name": "other", "status": "ready"},
		"environment_binding": managedEnvironmentBindingReceipt(manageEnvironmentsToolName, map[string]any{"implementation": "Engine B"}),
	})
	if (serverAgentRuntimeToolGateway{taskRun: run}).agentRuntimeSelectedImplementationEnvironmentPreflight("bash", map[string]any{"environment": "other"}) == nil {
		t.Fatal("canonical receipt weakened actual implementation selection")
	}
	if askUserImplementationIdentityMatches("Engine A", "Engine A 1.2.7") {
		t.Fatal("version identity matching was broadened")
	}
}
