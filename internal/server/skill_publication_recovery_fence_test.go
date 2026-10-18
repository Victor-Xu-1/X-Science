package server

import (
	"path/filepath"
	"testing"

	"x-science/internal/sciencecapability"
)

func TestPendingSkillPublicationCannotAuthorizeRetainedRuntimeCache(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	app, workspace := fixture.server, t.TempDir()
	const name = "retained-publication-fixture"
	archive := func(resource string) []byte {
		return buildSkillArchive(t, map[string]string{
			name + "/SKILL.md": "---\nname: " + name + "\ndescription: Publication lifecycle fixture.\n" +
				"preferred-execution-assets: [scripts/run.py]\n---\n\nRead ${X_SCIENCE_SKILL_DIR}/scripts/run.py.\n",
			name + "/scripts/run.py": resource,
		})
	}
	if _, err := app.importSkillBundle(name+".zip", "", false, archive("# Original confirmed fixture, never executed.\n")); err != nil {
		t.Fatal(err)
	}
	app.scienceCapabilities = &sciencecapability.Catalog{Capabilities: []sciencecapability.Definition{{
		ID: "publication-fixture", AcceptedEngines: []sciencecapability.EngineDefinition{{ID: "fixture", ExecutionPack: sciencecapability.ExecutionPack{
			ID: "publication.fixture", Mode: "local", Skill: name, Script: "fixture/run.py",
		}}},
	}}}
	run := &sessionRunnerChatRun{}
	run.addExecutedSkillNames(name)
	gateway := serverAgentRuntimeToolGateway{server: app, taskRun: run, kernel: &agentKernelContext{workspaceDir: workspace}}
	cacheCurrentDirectory := func() string {
		t.Helper()
		skill, found := findCatalogSkill(app.skillCatalog, name)
		if !found {
			t.Fatal("published catalog fixture missing")
		}
		bundle, resources, err := loadAgentSkillRuntimeBundle(skill)
		if err != nil || !resources {
			t.Fatalf("source bundle resources=%t err=%v", resources, err)
		}
		directory, err := materializeAgentSkillRuntimeBundle(workspace, name, bundle)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(filepath.Join(directory, "scripts/run.py"))
	}
	assertConfirmed := func(script string) {
		t.Helper()
		if !gateway.agentRuntimeCommandIsTrustedGenericMaterializedSkillExecution("bash", map[string]any{"command": "python " + script}) {
			t.Error("confirmed generic cached asset lost its existing admission")
		}
		if result := gateway.retainedSkillExecutionDiagnostic(name); result == nil || result["required_entrypoint"] != script {
			t.Errorf("confirmed retained entrypoint=%#v", result)
		}
	}
	assertConfirmed(cacheCurrentDirectory())
	prepared, err := app.prepareSkillBundle(name+".zip", "", true, archive("# Pending replacement fixture, never executed.\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	_, _, err = app.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			// Reproduce a cache left by the pre-fix selected-materialization bug.
			// Its digest/contents are valid, but no publication grant exists yet.
			script := cacheCurrentDirectory()
			if gateway.agentRuntimeCommandIsTrustedGenericMaterializedSkillExecution("bash", map[string]any{"command": "python " + script}) {
				t.Error("pending package cache became generic execution admission")
			}
			if result := gateway.retainedSkillExecutionDiagnostic(name); result != nil {
				t.Errorf("pending package cache became retained execution guidance: %#v", result)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	assertConfirmed(cacheCurrentDirectory())
}
