package server

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"x-science/internal/skills"
)

func TestManagedSkillSourceCatalogMatchesPublishedAuthority(t *testing.T) {
	for _, test := range []struct{ label, name, rootSuffix string }{
		{"package-marker", "x-science", ""},
		{"configured-root-marker", "managed-source-fixture", "runtime/assets/skills/host-data"},
	} {
		t.Run(test.label, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			if test.rootSuffix != "" {
				fixture.server.fileRoot = filepath.Join(fixture.server.fileRoot, filepath.FromSlash(test.rootSuffix))
				if err := os.MkdirAll(fixture.server.fileRoot, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			manifest := kernelSkillManifestForTest(test.name, "Published synthetic workflow.")
			if _, err := fixture.call("host.skills.edit", test.name, "SKILL.md", manifest); err != nil {
				t.Fatal(err)
			}
			published, err := fixture.publishReviewed(test.name, false)
			if err != nil {
				t.Fatal(err)
			}
			publishedID := stringValue(mapValue(published)["skill_id"])
			listed := compatJSONRequest(t, fixture.server.Handler(), http.MethodGet, "/api/skills/catalog", fixture.stream.OwnerID, nil, http.StatusOK)
			entry := compatibilityCatalogEntryByName(t, listed, test.name)
			if entry["source"] != "personal" || entry["skillId"] != publishedID {
				t.Errorf("catalog changed managed source/publication identity: %#v published=%q", entry, publishedID)
			}
			if read, err := fixture.call("host.skills.read", entry["skillId"]); err != nil || stringValue(mapValue(read)["content"]) != manifest {
				t.Errorf("catalog identity does not read its published package: %#v err=%v", read, err)
			}
			if _, err := fixture.call("host.skills.read", "bundled:"+test.name); err == nil {
				t.Error("managed source crossed into bundled namespace")
			}
			items, err := fixture.call("host.skills.list")
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items.([]map[string]any) {
				if item["name"] == test.name && item["origin"] != "personal" {
					t.Errorf("host list contradicted managed origin: %#v", item)
				}
			}
			validation, err := fixture.call("host.skills.validate", test.name)
			if err != nil || mapValue(validation)["origin"] != "personal" {
				t.Errorf("validation origin=%#v err=%v", validation, err)
			}
			skill, found := findCatalogSkill(fixture.server.skillCatalog, test.name)
			if !found {
				t.Fatal("published Skill missing from catalog")
			}
			if source := webSkillBridgeSource(fixture.server, skill); source != "custom" {
				t.Errorf("legacy web bridge source=%q", source)
			}
			if !slices.Contains(fixture.server.signedDiscoveryWithPersonalBindings(nil, []string{test.name}), test.name) {
				t.Error("explicitly bound managed procedure was omitted from discovery")
			}
			if slices.Contains(fixture.server.signedDiscoveryWithPersonalBindings(nil, nil), test.name) {
				t.Error("source label implicitly granted an unbound procedure")
			}
		})
	}
}

func TestManagedSkillSourceReplacementKeepsBundledReadOnly(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "x-science"
	manifest := kernelSkillManifestForTest(name, "Original managed package.")
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	replacement := kernelSkillManifestForTest(name, "Explicitly replaced managed package.")
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(replacement)); err == nil {
		t.Error("managed replacement did not require explicit overwrite")
	}
	if _, err := fixture.server.importSkillBundle(name+".md", "", true, []byte(replacement)); err != nil {
		t.Errorf("explicit managed replacement misclassified as bundled: %v", err)
	} else {
		assertKernelSkillReadback(t, fixture, "local:"+name, "SKILL.md", replacement)
	}
	const builtin = "readonly-source-fixture"
	fixture.server.skillCatalog.AddSkill(skills.Skill{Name: builtin, Path: "builtin:" + builtin, Body: "Original built-in."})
	if _, err := fixture.server.importSkillBundle(builtin+".md", "", true, []byte(kernelSkillManifestForTest(builtin, "Must not replace."))); err == nil {
		t.Error("a true bundled package became replaceable")
	}
	if current, _ := findCatalogSkill(fixture.server.skillCatalog, builtin); current.Body != "Original built-in." {
		t.Errorf("bundled bytes changed: %#v", current)
	}
}

func TestManagedSkillSourceIsNotReadOrDeleteAuthority(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	outside := t.TempDir()
	const name = "outside-source-fixture"
	manifest := kernelSkillManifestForTest(name, "Outside bytes must remain protected.")
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := skills.Load([]string{outside})
	if len(loaded.Skills()) != 1 {
		t.Fatalf("invalid isolated source fixture: %#v", loaded.LoadErrors())
	}
	fixture.server.skillCatalog.UpsertSkill(loaded.Skills()[0])
	if _, err := fixture.call("host.skills.read", "local:"+name); err == nil {
		t.Error("legacy personal display category granted an outside local read")
	}
	if _, err := fixture.call("host.skills.read", "bundled:"+name); err == nil {
		t.Error("unknown outside package became bundled")
	}
	if _, err := fixture.call("host.skills.delete", name); err == nil {
		t.Error("source display category granted outside deletion")
	}
	managed := personalSkillDirectory(fixture.server.fileRoot)
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(managed, name)); err != nil {
		t.Fatal(err)
	}
	linked := loaded.Skills()[0]
	linked.Path = filepath.Join(managed, name, "SKILL.md")
	fixture.server.skillCatalog.UpsertSkill(linked)
	if _, err := fixture.call("host.skills.read", "local:"+name); err == nil {
		t.Error("managed source label bypassed the regular-root/symlink fence")
	}
	if _, err := fixture.call("host.skills.delete", name); err == nil {
		t.Error("managed source label bypassed the delete root fence")
	}
	content, err := os.ReadFile(filepath.Join(outside, "SKILL.md"))
	if err != nil || string(content) != manifest {
		t.Fatalf("outside source changed: %q err=%v", content, err)
	}
	const draft = "source-draft-fixture"
	if _, err := fixture.call("host.skills.edit", draft, "SKILL.md", kernelSkillManifestForTest(draft, "Draft remains unpublished.")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.read", "local:"+draft); err == nil {
		t.Error("draft became published because of a managed source label")
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, draft); found {
		t.Error("draft leaked into the ordinary catalog")
	}
	if !strings.HasPrefix(linked.Path, managed+string(filepath.Separator)) {
		t.Fatal("symlink fixture did not use the managed boundary")
	}
}
