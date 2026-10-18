package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

type kernelSkillDraftFixture struct {
	*agentSaveArtifactsFixture
	bound      kernelHostExecutionIdentity
	generation uint64
	sequence   int
}

func newKernelSkillDraftFixture(t *testing.T) *kernelSkillDraftFixture {
	t.Helper()
	fixture := &kernelSkillDraftFixture{agentSaveArtifactsFixture: newAgentSaveArtifactsFixture(t), generation: 1}
	fixture.bound = kernelHostExecutionIdentity{
		access: fixture.identity.access,
		transcriptExecution: kernelTranscriptExecutionBinding{
			stream: fixture.stream, claim: fixture.claim,
			outerToolUseID: "skill-authoring-cell", kernelID: "skill-authoring-kernel", kernelGeneration: 1,
			currentKernelGeneration: func() uint64 { return fixture.generation },
		},
	}
	return fixture
}

func (fixture *kernelSkillDraftFixture) call(method string, args ...any) (any, error) {
	fixture.sequence++
	return fixture.server.handleKernelHostCall(fixture.bound, nil)(context.Background(), kernelruntime.HostCall{
		ID: fmt.Sprintf("hc-%032x", fixture.sequence), CellID: "skill-authoring-cell", Method: method, Args: args,
	})
}

func kernelSkillManifestForTest(name, body string) string {
	return "---\nname: " + name + "\ndescription: Bounded synthetic Skill fixture.\n---\n\n" + body + "\n"
}

func TestKernelSkillDraftIsAuthorVisibleButNotDiscoverableAcrossRestart(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "draft-isolation-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Read the fixture.")); err != nil {
		t.Fatal(err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("unpublished draft leaked into the ordinary catalog")
	}
	if _, err := fixture.server.executeSkillTool(map[string]any{"skill": name}); err == nil {
		t.Fatal("ordinary Skill invocation accepted an unpublished draft")
	}
	listed, err := fixture.call("host.skills.list")
	if err != nil {
		t.Fatal(err)
	}
	foundDraft := false
	for _, entry := range listed.([]map[string]any) {
		if entry["name"] == name && entry["origin"] == "draft" {
			foundDraft = true
		}
	}
	if !foundDraft {
		t.Fatal("authoring list omitted the draft")
	}
	read, err := fixture.call("host.skills.read", name)
	if err != nil || !strings.Contains(stringValue(mapValue(read)["content"]), "Read the fixture.") {
		t.Fatalf("draft read=%#v err=%v", read, err)
	}
	legacy := filepath.Join(fixture.server.fileRoot, "skills", "legacy-draft-fixture")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, compatibilitySkillDraftMarker), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "SKILL.md"), []byte(kernelSkillManifestForTest("legacy-draft-fixture", "Legacy draft.")), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, _, failures := loadSkillCatalog(nil, nil, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("catalog reconstruction failures=%#v", failures)
	}
	for _, draftName := range []string{name, "legacy-draft-fixture"} {
		if _, found := findCatalogSkill(catalog, draftName); found {
			t.Fatalf("restart loaded draft %q", draftName)
		}
	}
}

func TestKernelSkillValidateChecksPackageWithoutExecutingHelpers(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "validate-draft-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Inspect scripts/helper.py before use.")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "helper-must-not-run")
	helper := "from pathlib import Path\nPath(" + fmt.Sprintf("%q", marker) + ").write_text('must not execute')\n"
	for path, content := range map[string]string{
		"scripts/helper.py": helper, "references/guide.md": "Fixture reference.\n", "assets/example.txt": "Fixture asset.\n",
	} {
		if _, err := fixture.call("host.skills.edit", name, path, content); err != nil {
			t.Fatalf("edit %s: %v", path, err)
		}
	}
	result, err := fixture.call("host.skills.validate", name)
	if err != nil || mapValue(result)["valid"] != true {
		t.Fatalf("structural validation result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("validation executed helper, stat=%v", err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("validation published the draft")
	}
	if _, err := fixture.call("host.skills.publish", name, false); err != nil {
		t.Fatal(err)
	}
	published, found := findCatalogSkill(fixture.server.skillCatalog, name)
	if !found {
		t.Fatal("valid publication is missing")
	}
	live, err := os.ReadFile(published.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Updated draft.", "Inspect scripts/helper.py before use."); err != nil {
		t.Fatal(err)
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("draft=%#v found=%t err=%v", draft, found, err)
	}
	for _, invalid := range []struct{ label, manifest string }{
		{"missing_name", "---\ndescription: Explicit description\n---\nPrivate fixture text"},
		{"missing_description", "---\nname: " + name + "\n---\nPrivate fixture text"},
		{"non_string_description", "---\nname: " + name + "\ndescription: 42\n---\nPrivate fixture text"},
		{"non_string_name", "---\nname: 42\ndescription: Explicit description\n---\nPrivate fixture text"},
		{"case_mismatch", kernelSkillManifestForTest(strings.ToUpper(name), "Private fixture text")},
		{"unclosed_frontmatter", "---\nname: " + name + "\ndescription: Explicit description\nPrivate fixture text"},
		{"invalid_utf8", kernelSkillManifestForTest(name, "Private fixture text") + string([]byte{0xff})},
		{"nul", kernelSkillManifestForTest(name, "Private fixture text") + "\x00"},
	} {
		t.Run(invalid.label, func(t *testing.T) {
			if err := os.WriteFile(draft.Path, []byte(invalid.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			report, err := fixture.call("host.skills.validate", name)
			if err != nil || mapValue(report)["valid"] != false || strings.Contains(fmt.Sprint(report), "Private fixture text") {
				t.Fatalf("invalid package validation=%#v err=%v", report, err)
			}
			if _, err := fixture.call("host.skills.publish", name, true); err == nil {
				t.Fatal("invalid draft was published")
			}
			current, err := os.ReadFile(published.Path)
			if err != nil || string(current) != string(live) {
				t.Fatalf("rejected publish changed live bytes: %v", err)
			}
			catalog, found := findCatalogSkill(fixture.server.skillCatalog, name)
			if !found || catalog.Name != published.Name || catalog.Path != published.Path || catalog.BodyHash != published.BodyHash {
				t.Fatalf("rejected publish changed catalog identity/content: %#v", catalog)
			}
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid validation or publication executed helper, stat=%v", err)
	}
}

func TestKernelSkillPublishedEditRemainsDraftUntilExplicitValidatedPublish(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "published-edit-fixture"
	manifest := kernelSkillManifestForTest(name, "Original published workflow.")
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Updated draft workflow.", "Original published workflow."); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(livePath)
	if err != nil || string(live) != manifest {
		t.Fatalf("editing changed published bytes=%q err=%v", live, err)
	}
	published, found := findCatalogSkill(fixture.server.skillCatalog, name)
	if !found || !strings.Contains(published.Body, "Original published workflow.") {
		t.Fatalf("editing changed live catalog=%#v", published)
	}
	restarted, _, failures := loadSkillCatalog(nil, nil, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("draft/live restart failures=%#v", failures)
	}
	restartedPublished, found := findCatalogSkill(restarted, name)
	if !found || !strings.Contains(restartedPublished.Body, "Original published workflow.") {
		t.Fatalf("draft shadowed published package after restart=%#v", restartedPublished)
	}
	read, err := fixture.call("host.skills.read", name)
	if err != nil || !strings.Contains(stringValue(mapValue(read)["content"]), "Updated draft workflow.") {
		t.Fatalf("author read=%#v err=%v", read, err)
	}
	if _, err := fixture.call("host.skills.publish", name, false); err == nil {
		t.Fatal("publish overwrote an existing package without explicit overwrite")
	}
	live, err = os.ReadFile(livePath)
	if err != nil || string(live) != manifest {
		t.Fatalf("conflicted publish changed original=%q err=%v", live, err)
	}
	if _, err := fixture.call("host.skills.publish", name, true); err != nil {
		t.Fatal(err)
	}
	live, err = os.ReadFile(livePath)
	if err != nil || !strings.Contains(string(live), "Updated draft workflow.") {
		t.Fatalf("published result=%q err=%v", live, err)
	}
	published, found = findCatalogSkill(fixture.server.skillCatalog, name)
	if !found || !strings.Contains(published.Body, "Updated draft workflow.") {
		t.Fatalf("published catalog=%#v", published)
	}
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Discard this subsequent draft.", "Updated draft workflow."); err != nil {
		t.Fatal(err)
	}
	deleted, err := fixture.call("host.skills.delete", name)
	if err != nil || mapValue(deleted)["unpublished"] != false {
		t.Fatalf("delete draft=%#v err=%v", deleted, err)
	}
	live, err = os.ReadFile(livePath)
	if err != nil || !strings.Contains(string(live), "Updated draft workflow.") {
		t.Fatalf("draft deletion changed published bytes=%q err=%v", live, err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); !found {
		t.Fatal("draft deletion removed the live catalog entry")
	}
}
