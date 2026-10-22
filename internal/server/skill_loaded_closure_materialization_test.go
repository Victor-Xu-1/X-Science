package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/persistence/workspace"
	"x-science/internal/tools/registry"
)

func TestSkillClosureMaterializationPinsResourceOnlyPublishedPackage(t *testing.T) {
	fixture := resourceLoadedClosureFixture(t)
	const name, resource = "resource-only-pinned-fixture", "scripts/route.py"
	manifest := "---\nname: " + name + "\ndescription: Resource-only snapshot fixture.\npreferred-execution-assets: [" + resource + "]\n---\n\nUse the bundled implementation route.\n"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, resource, "print('ORIGINAL_RESOURCE')\n"); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	snapshot := selectedLoadedClosureFixture(t, fixture, name)
	assertLoadedResourceFixture(t, snapshot, name, resource, false)
	run, _ := loadedClosureFixtureGateway(fixture)
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	prepared, err := snapshot.materialize(ctx, fixture.server, run.SessionID)
	if err != nil || len(prepared) != 1 {
		t.Fatalf("materialize actual captured package: %v", err)
	}
	if _, err := fixture.call("host.skills.edit", name, resource, "print('REVISED_RESOURCE')\n", "print('ORIGINAL_RESOURCE')\n"); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, true)
	current := selectedLoadedClosureFixture(t, fixture, name)
	if current.contracts[0].bundle.digest == snapshot.contracts[0].bundle.digest {
		t.Fatal("fixture did not actually publish distinct resource bytes")
	}
	loadedBytes, err := os.ReadFile(filepath.Join(filepath.Dir(prepared[0].Path), filepath.FromSlash(resource)))
	if err != nil || !strings.Contains(string(loadedBytes), "ORIGINAL_RESOURCE") || strings.Contains(string(loadedBytes), "REVISED_RESOURCE") {
		t.Fatalf("captured v1 runtime path followed mutable v2 publication: bytes=%q err=%v", loadedBytes, err)
	}
	if !strings.Contains(prepared[0].Path, filepath.Join(".x-science", "runtime", "skills")) {
		t.Fatal("resource-only package did not use the existing immutable workspace materializer")
	}
}

func TestSkillClosureSelectedRunnerContextResolvesCapturedRuntimeDirectory(t *testing.T) {
	fixture := resourceLoadedClosureFixture(t)
	// Use the actual production registry; a nil-registry fixture correctly
	// filters every Skill before the selected-context consumer can run.
	fixture.server.tools = registry.Default()
	const name, resource = "selected-runtime-render-fixture", "scripts/route.py"
	manifest := "---\nname: " + name + "\ndescription: First-request runtime context fixture.\npreferred-execution-assets: [" + resource + "]\n---\n\nInspect ${X_SCIENCE_SKILL_DIR}/" + resource + " before using this package.\n"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, resource, "print('CAPTURED_RUNTIME')\n"); err != nil {
		t.Fatal(err)
	}
	publishLoadedClosureFixture(t, fixture, name, false)
	assertLoadedResourceFixture(t, selectedLoadedClosureFixture(t, fixture, name), name, resource, true)
	enabled := true
	if _, err := fixture.store.CreateAgent(workspace.CreateAgentInput{
		ID: "selected-runtime-fixture-agent", UserID: fixture.stream.OwnerID, Name: "OPERON",
		DisplayName: "Selected runtime fixture", Enabled: &enabled, Unrestricted: true, SkillNames: []string{name},
		Description: "Preserves the actual owner-selected resource contract.", SystemPrompt: "SELECTED_RUNTIME_OWNER_FIXTURE",
	}); err != nil {
		t.Fatal(err)
	}
	run, _ := loadedClosureFixtureGateway(fixture)
	ctx := withTranscriptRunnerChatRun(context.Background(), run)
	entries, err := fixture.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	session, err := fixture.server.loadTranscriptFrameSessionProjection(fixture.stream)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools    []any `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var system strings.Builder
		for _, message := range request.Messages {
			if message.Role == "system" {
				system.WriteString(message.Content)
			}
		}
		if hasChatToolNamed(request.Tools, "skill") {
			select {
			case observed <- system.String():
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"The bounded fixture request was observed."},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	options := normalizeSessionRunnerChatOptions(SessionRunnerChatOptions{
		SessionID: run.SessionID, RunnerID: run.Transcript.Claim.RunnerID,
		Endpoint: provider.URL + "/v1/chat/completions", Model: "captured-context-fixture",
		AllowedTools: []string{"skill"}, DisableMCPDiscovery: true, DisableSkillDiscovery: true,
		SelectedSkillNames: []string{name}, RequestTimeout: 5 * time.Second, PreparationTimeout: 20 * time.Second,
		MaxToolRounds: 1, MaxAttempts: 1, ReplayLimit: 20, OutputLimitBytes: 64 << 10,
	})
	bound, err := fixture.server.bindSessionRunnerToolAuthority(ctx, session, options)
	if err != nil || strings.Join(bound.Options.SelectedSkillNames, ",") != name {
		t.Fatalf("actual owner profile did not select the fixture Skill: selected=%v err=%v", bound.Options.SelectedSkillNames, err)
	}
	selected, err := fixture.server.runtimeSkillsByNameWithConnectorSchemas(bound.Options.SelectedSkillNames, bound.Options.ExcludedSkillNames, bound.Schemas)
	if err != nil {
		t.Fatal(err)
	}
	liveTools := filterAgentRuntimeToolSchemas(bound.Schemas, bound.Options.AllowedTools)
	retained := fixture.server.agentRuntimeDiscoverableSkills(selected, agentRuntimeToolSchemaNameSet(liveTools))
	if fixture.server.tools == nil || len(retained) != 1 || retained[0].Name != name {
		t.Fatalf("actual registry/tool authority filtered selected fixture: selected=%d retained=%d", len(selected), len(retained))
	}
	_, runnerErr := fixture.server.runSessionRunnerChat(ctx, options, session, entries, run)
	select {
	case system := <-observed:
		// This assertion exercises the actual selected-system-context consumer,
		// not a hand-rendered stand-in or a successful scientific task claim.
		if !strings.Contains(system, "SELECTED_RUNTIME_OWNER_FIXTURE") || !strings.Contains(system, "### "+name) || !strings.Contains(system, "Inspect ") {
			t.Fatalf("actual provider request omitted the selected contract; selected=%d retained=%d owner=%t heading=%t body=%t runnerErr=%v",
				len(selected), len(retained), strings.Contains(system, "SELECTED_RUNTIME_OWNER_FIXTURE"), strings.Contains(system, "### "+name), strings.Contains(system, "Inspect "), runnerErr)
		}
		if strings.Contains(system, "${X_SCIENCE_SKILL_DIR}") {
			t.Fatal("actual selected system context retained an unresolved runtime directory")
		}
		if !strings.Contains(system, "/.x-science/runtime/skills/") || !strings.Contains(system, "/scripts/route.py") {
			t.Fatal("actual selected system context omitted the captured content-addressed resource route")
		}
	default:
		t.Fatalf("actual selected context never reached the controlled provider: %v", runnerErr)
	}
}

func resourceLoadedClosureFixture(t *testing.T) *kernelSkillDraftFixture {
	t.Helper()
	store, manager, app, _ := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "resource-runtime.db"), true)
	t.Cleanup(func() { closeKernelHostTestRuntime(t, app, manager, store) })
	fixture := &kernelSkillDraftFixture{
		agentSaveArtifactsFixture: newAgentSaveArtifactsFixtureWithKernelManager(t, manager), generation: 1,
	}
	fixture.bound = kernelHostExecutionIdentity{
		access: fixture.identity.access,
		transcriptExecution: kernelTranscriptExecutionBinding{
			stream: fixture.stream, claim: fixture.claim, outerToolUseID: "skill-authoring-cell",
			kernelID: "skill-authoring-kernel", kernelGeneration: 1,
			currentKernelGeneration: func() uint64 { return fixture.generation },
		},
	}
	if !fixture.server.kernelConfinementEvidence(false).Available {
		t.Fatal("existing local resource-materialization fixture lacks its required confinement evidence")
	}
	return fixture
}

func assertLoadedResourceFixture(t *testing.T, snapshot runtimeSkillClosureSnapshot, name, resource string, placeholder bool) {
	t.Helper()
	if len(snapshot.contracts) != 1 {
		t.Fatal("fixture did not select one actual published contract")
	}
	contract := snapshot.contracts[0]
	if contract.skill.Name != name || contract.bundleKind != "files" || !contract.hasResources || len(contract.bundle.files) != 2 ||
		len(contract.skill.PreferredExecutionAssets) != 1 || contract.skill.PreferredExecutionAssets[0] != resource ||
		strings.Contains(contract.skill.Body, "${X_SCIENCE_SKILL_DIR}") != placeholder {
		t.Fatal("actual parsed resource/preferred-asset fixture facts do not match the intended consumer regression")
	}
}
