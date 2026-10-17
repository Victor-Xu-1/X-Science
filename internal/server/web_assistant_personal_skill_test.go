package server

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/skills"
)

const boundPersonalFixtureSkill = "personal-file-review"

func boundPersonalSkillFixture(t *testing.T) (*Server, workspace.CompatibilityFrame) {
	t.Helper()
	store, err := workspace.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "local", Name: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(workspace.CreateFrameInput{ID: "frame", ProjectID: "project", AgentName: "OPERON", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	app := newV11TestServer(t, Options{Workspace: store, FileRoot: t.TempDir()})
	app.skillCatalog.UpsertSkill(skills.Skill{Name: boundPersonalFixtureSkill, Description: "Review existing files.", Path: filepath.Join(t.TempDir(), boundPersonalFixtureSkill, "SKILL.md"), Body: "Read existing files and report differences."})
	if _, err := store.SetFrameRuntimeMetadata("frame", workspace.FrameRuntimeMetadata{ContextData: map[string]any{"web_assistant": map[string]any{"id": webAssistantID("OPERON")}}}); err != nil {
		t.Fatal(err)
	}
	return app, workspace.CompatibilityFrame{Frame: workspace.Frame{ID: "frame", AgentName: "OPERON", ProjectID: "project"}}
}

func attachPersonalSkillFixture(t *testing.T, app *Server) {
	t.Helper()
	result := agentCompatJSON(t, app.Handler(), http.MethodPut, "/api/x-science/expert-profiles/OPERON/skills", map[string]any{"attach": []string{boundPersonalFixtureSkill}, "detach": []string{}}, http.StatusOK).(map[string]any)
	if !slices.Contains(anyStringSlice(t, result["skillNames"]), boundPersonalFixtureSkill) {
		t.Fatal("binding response lost requested personal Skill")
	}
}

func TestBoundPersonalSkillAdmissionAcrossComposerSendRuntime(t *testing.T) {
	app, frame := boundPersonalSkillFixture(t)
	attachPersonalSkillFixture(t, app)
	capabilities, err := app.webConversationComposerCapabilities("local", frame)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(capabilities["skills"].([]string), boundPersonalFixtureSkill) {
		t.Fatal("committed personal binding is missing from conversation admission")
	}
	input := &webConversationSendInput{InjectSkills: []string{boundPersonalFixtureSkill}}
	if err := app.validateWebConversationSendInput("local", frame, input); err != nil {
		t.Fatalf("composer-admitted personal Skill rejected by Send: %v", err)
	}
	session := sessionstore.Session{ID: frame.ID, Orchestration: map[string]any{"inputData": map[string]any{"inject_skills": []string{boundPersonalFixtureSkill}}}}
	options, _, err := app.applyWebAssistantRuntimeOptions(session, "local", "OPERON", SessionRunnerChatOptions{AllowedTools: []string{"fixture-safe-tool"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(options.SelectedSkillNames, []string{boundPersonalFixtureSkill}) || !slices.Contains(options.AllowedSkillNames, boundPersonalFixtureSkill) {
		t.Fatalf("runtime did not preserve explicit personal selection: selected=%v allowed=%v", options.SelectedSkillNames, options.AllowedSkillNames)
	}
	if !slices.Equal(options.AllowedTools, []string{"fixture-safe-tool"}) {
		t.Fatal("Skill binding changed tool authority")
	}
	options, _, err = app.applyWebAssistantRuntimeOptions(sessionstore.Session{ID: frame.ID}, "local", "OPERON", SessionRunnerChatOptions{})
	if err != nil || len(options.SelectedSkillNames) != 0 {
		t.Fatalf("binding injected Skills automatically: selected=%v err=%v", options.SelectedSkillNames, err)
	}
}

func TestBoundPersonalSkillAdmissionKeepsOwnerAndSignedBoundaries(t *testing.T) {
	app, frame := boundPersonalSkillFixture(t)
	before, _, err := app.webAssistantRecord("local", webAssistantID("OPERON"))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(app.webAssistantAllowedSkillNames(before), boundPersonalFixtureSkill) {
		t.Fatal("globally installed personal Skill was auto-admitted without a binding")
	}
	attachPersonalSkillFixture(t, app)
	other, _, err := app.webAssistantRecord("other-owner", webAssistantID("OPERON"))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(app.webAssistantAllowedSkillNames(other), boundPersonalFixtureSkill) {
		t.Fatal("personal binding crossed owner boundary")
	}
	app.skillCatalog.UpsertSkill(skills.Skill{Name: "stale-builtin-fixture", Path: "builtin:fixture"})
	if _, err := app.workspaceStore.UpdateAgentSkills("local", "OPERON", []string{"stale-builtin-fixture", "missing-personal-fixture"}, nil); err != nil {
		t.Fatal(err)
	}
	after, _, err := app.webAssistantRecord("local", webAssistantID("OPERON"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := app.webAssistantAllowedSkillNames(after)
	if slices.Contains(allowed, "stale-builtin-fixture") || slices.Contains(allowed, "missing-personal-fixture") {
		t.Fatal("stale unsigned builtin or missing Skill was re-admitted from a legacy profile")
	}
	for _, name := range before.Agent.SkillNames {
		if !slices.Contains(allowed, name) {
			t.Fatalf("current signed default %q was lost", name)
		}
	}
	if _, err := app.workspaceStore.UpdateAgentSkills("local", "OPERON", nil, []string{boundPersonalFixtureSkill}); err != nil {
		t.Fatal(err)
	}
	if err := app.validateWebConversationSendInput("local", frame, &webConversationSendInput{InjectSkills: []string{boundPersonalFixtureSkill}}); err == nil {
		t.Fatal("detached personal Skill remained callable")
	}
}

func TestBoundPersonalSkillAdmissionKeepsAssistantExclusion(t *testing.T) {
	app, frame := boundPersonalSkillFixture(t)
	attachPersonalSkillFixture(t, app)
	if err := app.setWebAssistantConfig("local", "OPERON", webAssistantStoredConfig{DisabledBuiltinSkills: []string{boundPersonalFixtureSkill}}); err != nil {
		t.Fatal(err)
	}
	capabilities, err := app.webConversationComposerCapabilities("local", frame)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(capabilities["skills"].([]string), boundPersonalFixtureSkill) {
		t.Fatal("binding bypassed an assistant exclusion")
	}
	if err := app.validateWebConversationSendInput("local", frame, &webConversationSendInput{InjectSkills: []string{boundPersonalFixtureSkill}}); err == nil {
		t.Fatal("Send bypassed an assistant exclusion")
	}
}
