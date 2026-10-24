package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireRepairableKernelSkillDraft(t *testing.T, fixture *kernelSkillDraftFixture, name, content string) {
	t.Helper()
	read, err := fixture.call("host.skills.read", name)
	if err != nil || stringValue(mapValue(read)["content"]) != content {
		t.Fatalf("draft read changed: result=%#v err=%v", read, err)
	}
	listed, err := fixture.call("host.skills.list")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range listed.([]map[string]any) {
		if entry["name"] == name && entry["origin"] == "draft" {
			found = true
		}
	}
	if !found {
		t.Fatal("incomplete draft was not author-visible")
	}
	validation, err := fixture.call("host.skills.validate", name)
	if err != nil || mapValue(validation)["valid"] != false {
		t.Fatalf("incomplete draft passed validation: result=%#v err=%v", validation, err)
	}
	diagnostics, ok := mapValue(validation)["errors"].([]map[string]string)
	if !ok || len(diagnostics) != 1 || diagnostics[0]["code"] == "invalid_package" ||
		(!strings.Contains(diagnostics[0]["message"], "frontmatter") && !strings.Contains(diagnostics[0]["message"], "required")) {
		t.Fatalf("draft validation lacks a safe repair reason: %#v", validation)
	}
}

func TestKernelSkillIncompleteDraftCanBeRepairedBeforePublication(t *testing.T) {
	for _, initial := range []string{"Draft instructions without frontmatter.\n", "---\nname: [unfinished\n", "---\nname: repairable-draft-fixture\n---\nDescription still required.\n"} {
		t.Run(fmt.Sprintf("manifest-%d", len(initial)), func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "repairable-draft-fixture"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", initial); err != nil {
				t.Fatalf("save incomplete draft: %v", err)
			}
			for path, content := range map[string]string{
				"scripts/helper.py":   "raise RuntimeError('validation must not execute this helper')\n",
				"references/guide.md": "Draft reference.\n", "assets/example.txt": "Draft asset.\n",
				"catalog-ui.json": fmt.Sprintf(`{"schema_version":1,"skills":{"%s":{"category":"workflow","description_i18n":{"en":"Draft fixture"}}}}`, name),
			} {
				if _, err := fixture.call("host.skills.edit", name, path, content); err != nil {
					t.Fatalf("save resource %s: %v", path, err)
				}
			}
			requireRepairableKernelSkillDraft(t, fixture, name, initial)
			if _, err := fixture.publishReviewed(name, false); err == nil {
				t.Fatal("incomplete draft was published")
			}
			if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
				t.Fatal("incomplete draft entered the live catalog")
			}
			liveRoot := filepath.Join(fixture.server.fileRoot, "skills", name)
			if _, err := os.Stat(liveRoot); !os.IsNotExist(err) {
				t.Fatalf("incomplete draft created a live directory: %v", err)
			}
			requireRepairableKernelSkillDraft(t, fixture, name, initial)
			manifest := kernelSkillManifestForTest(name, "Repaired reusable workflow.")
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest, initial); err != nil {
				t.Fatalf("repair draft: %v", err)
			}
			validation, err := fixture.call("host.skills.validate", name)
			if err != nil || mapValue(validation)["valid"] != true {
				t.Fatalf("repaired validation=%#v err=%v", validation, err)
			}
			if _, err := fixture.publishReviewed(name, false); err != nil {
				t.Fatal(err)
			}
			published, found := findCatalogSkill(fixture.server.skillCatalog, name)
			if !found || !strings.Contains(published.Body, "Repaired reusable workflow.") {
				t.Fatalf("published=%#v found=%t", published, found)
			}
			for _, path := range []string{"scripts/helper.py", "references/guide.md", "assets/example.txt"} {
				if _, err := os.Stat(filepath.Join(liveRoot, filepath.FromSlash(path))); err != nil {
					t.Fatalf("published resource %s: %v", path, err)
				}
			}
		})
	}
}

func TestKernelSkillInvalidEditedDraftPreservesPublishedPackageUntilRepair(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "published-draft-repair-fixture"
	original := kernelSkillManifestForTest(name, "Original published workflow.")
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(original)); err != nil {
		t.Fatal(err)
	}
	const incomplete = "---\nname: [temporarily unfinished\n"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", incomplete, original); err != nil {
		t.Fatalf("stage invalid edit: %v", err)
	}
	requireRepairableKernelSkillDraft(t, fixture, name, incomplete)
	if _, err := fixture.publishReviewed(name, true); err == nil {
		t.Fatal("invalid edited draft replaced the published package")
	}
	livePath := filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md")
	live, err := os.ReadFile(livePath)
	if err != nil || string(live) != original {
		t.Fatalf("invalid draft changed live bytes=%q err=%v", live, err)
	}
	published, found := findCatalogSkill(fixture.server.skillCatalog, name)
	if !found || !strings.Contains(published.Body, "Original published workflow.") {
		t.Fatalf("invalid draft changed live catalog=%#v", published)
	}
	manifest := kernelSkillManifestForTest(name, "Corrected published workflow.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest, incomplete); err != nil {
		t.Fatalf("repair edited draft: %v", err)
	}
	validation, err := fixture.call("host.skills.validate", name)
	if err != nil || mapValue(validation)["valid"] != true {
		t.Fatalf("repaired validation=%#v err=%v", validation, err)
	}
	if _, err := fixture.publishReviewed(name, false); err == nil {
		t.Fatal("repair bypassed the explicit overwrite contract")
	}
	if _, err := fixture.publishReviewed(name, true); err != nil {
		t.Fatal(err)
	}
	live, err = os.ReadFile(livePath)
	if err != nil || string(live) != manifest {
		t.Fatalf("repaired publication=%q err=%v", live, err)
	}
}

func TestKernelSkillResourcesFirstDraftRequiresExplicitManifestAndPublication(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "resources-first-draft-fixture"
	const helper = "raise RuntimeError('package validation must not execute helpers')\n"
	created, err := fixture.call("host.skills.edit", name, "scripts/helper.py", helper)
	if err != nil || mapValue(created)["action"] != "created" || mapValue(created)["path"] != "scripts/helper.py" {
		t.Fatalf("resources-first edit=%#v err=%v", created, err)
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("resources-first draft found=%t err=%v", found, err)
	}
	if _, err := os.Stat(draft.Path); !os.IsNotExist(err) {
		t.Fatalf("resource edit fabricated SKILL.md: %v", err)
	}
	read, err := fixture.call("host.skills.read", name, "scripts/helper.py")
	if err != nil || stringValue(mapValue(read)["content"]) != helper {
		t.Fatalf("resources-first read=%#v err=%v", read, err)
	}
	listed, err := fixture.call("host.skills.list")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, entry := range listed.([]map[string]any) {
		if entry["name"] == name && entry["origin"] == "draft" {
			found = true
		}
	}
	if !found {
		t.Fatal("resources-first draft was not author-visible")
	}
	validation, err := fixture.call("host.skills.validate", name)
	diagnostics, ok := mapValue(validation)["errors"].([]map[string]string)
	if err != nil || mapValue(validation)["valid"] != false || !ok || len(diagnostics) != 1 || diagnostics[0]["code"] != "missing_skill_document" {
		t.Fatalf("missing manifest diagnostic=%#v err=%v", validation, err)
	}
	if _, err := fixture.publishReviewed(name, false); err == nil {
		t.Fatal("resources-only draft was published")
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("resources-only draft entered the live catalog")
	}
	manifest := kernelSkillManifestForTest(name, "Completed resources-first workflow.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	validation, err = fixture.call("host.skills.validate", name)
	if err != nil || mapValue(validation)["valid"] != true {
		t.Fatalf("completed draft validation=%#v err=%v", validation, err)
	}
	if _, err := fixture.publishReviewed(name, false); err != nil {
		t.Fatal(err)
	}
	liveHelper, err := os.ReadFile(filepath.Join(fixture.server.fileRoot, "skills", name, "scripts", "helper.py"))
	if err != nil || string(liveHelper) != helper {
		t.Fatalf("published helper=%q err=%v", liveHelper, err)
	}
	for _, test := range []struct{ name, path, content, old string }{
		{"failed-resource-first-sidecar", "catalog-ui.json", `{"schema_version":`, ""},
		{"failed-resource-first-replacement", "scripts/helper.py", helper, "not-an-existing-file"},
	} {
		if _, err := fixture.call("host.skills.edit", test.name, test.path, test.content, test.old); err == nil {
			t.Fatalf("invalid first resource %s was accepted", test.name)
		}
		if _, found, err := fixture.server.findCompatibilitySkillDraft(test.name); err != nil || found {
			t.Fatalf("failed first resource left a canonical draft: found=%t err=%v", found, err)
		}
	}
}

func TestKernelSkillDraftPrivateControlPathsAreNotAuthorResources(t *testing.T) {
	for _, path := range []string{compatibilitySkillDraftMarker, "./" + compatibilitySkillDraftMarker, strings.ToUpper(compatibilitySkillDraftMarker), "references/.."} {
		t.Run(path, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "reserved-draft-resource-fixture"
			if _, err := fixture.call("host.skills.edit", name, path, "requested author content"); err == nil {
				t.Fatal("reserved first resource was reported as a successful write")
			}
			if _, found, err := fixture.server.findCompatibilitySkillDraft(name); err != nil || found {
				t.Fatalf("rejected first resource left a canonical draft: found=%t err=%v", found, err)
			}
		})
	}
	fixture := newKernelSkillDraftFixture(t)
	const name = "existing-private-draft-fixture"
	manifest := kernelSkillManifestForTest(name, "Private control files are not author resources.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("draft unavailable: found=%t err=%v", found, err)
	}
	markerPath := filepath.Join(filepath.Dir(draft.Path), compatibilitySkillDraftMarker)
	before, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, compatibilitySkillDraftMarker, "requested author content", string(before)); err == nil {
		t.Fatal("existing private draft marker was author-editable")
	}
	after, err := os.ReadFile(markerPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("private marker changed: after=%q err=%v", after, err)
	}
	read, err := fixture.call("host.skills.read", name)
	if err != nil || stringValue(mapValue(read)["content"]) != manifest {
		t.Fatalf("rejected control-file edit changed the manifest: result=%#v err=%v", read, err)
	}
}

func TestKernelSkillDraftRepairRetainsResourceSafety(t *testing.T) {
	for _, unsafe := range []string{"symbolic_link", "hard_link", "sidecar_during_incomplete_manifest", "directory_depth", "file_budget", "byte_budget"} {
		t.Run(unsafe, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "draft-resource-safety-fixture"
			manifest := kernelSkillManifestForTest(name, "Resource safety fixture.")
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
				t.Fatal(err)
			}
			draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
			if err != nil || !found {
				t.Fatalf("draft found=%t err=%v", found, err)
			}
			root := filepath.Dir(draft.Path)
			path, content := "references/not-committed.md", "Safe text must not bypass unsafe package resources.\n"
			switch unsafe {
			case "symbolic_link", "hard_link":
				outside := filepath.Join(t.TempDir(), "outside.txt")
				if err := os.WriteFile(outside, []byte("untouched external fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "external-link.txt")
				if unsafe == "hard_link" {
					err = os.Link(outside, link)
				} else {
					err = os.Symlink(outside, link)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					raw, err := os.ReadFile(outside)
					if err != nil || string(raw) != "untouched external fixture" {
						t.Errorf("unsafe link changed external bytes=%q err=%v", raw, err)
					}
				})
			case "sidecar_during_incomplete_manifest":
				if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Incomplete draft text.\n", manifest); err != nil {
					t.Fatal(err)
				}
				path, content = "catalog-ui.json", `{"schema_version":1,"schema_version":1,"skills":{}}`
			case "directory_depth":
				path = strings.Repeat("level/", 9) + "not-committed.md"
			case "file_budget":
				// SKILL.md and the draft marker already consume two entries.
				for index := 0; index < maxSkillArchiveFiles-2; index++ {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("resource-%d.txt", index)), []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "byte_budget":
				file, err := os.Create(filepath.Join(root, "opaque-asset.bin"))
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(maxSkillArchiveBytes - int64(len(manifest)+len("draft\n"))); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.call("host.skills.edit", name, path, content); err == nil {
				t.Fatalf("unsafe resource %s was accepted", unsafe)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
				t.Fatalf("rejected resource survived: %v", err)
			}
			if unsafe == "directory_depth" {
				if _, err := os.Stat(filepath.Join(root, "level")); !os.IsNotExist(err) {
					t.Fatalf("depth rejection created directories: %v", err)
				}
			}
			if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
				t.Fatal("resource rejection published the draft")
			}
			if unsafe == "symbolic_link" || unsafe == "hard_link" {
				validation, err := fixture.call("host.skills.validate", name)
				if err != nil || mapValue(validation)["valid"] != false || strings.Contains(fmt.Sprint(validation), root) {
					t.Fatalf("unsafe resource diagnostic leaked a machine path or hid failure: result=%#v err=%v", validation, err)
				}
			}
		})
	}
}
