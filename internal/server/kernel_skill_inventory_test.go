package server

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/skills"
)

func TestKernelSkillReadManifestInventoryUsesSelectedPackage(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "package-inventory-fixture"
	manifest := kernelSkillManifestForTest(name, "Original package instructions.")
	for path, content := range map[string]string{
		"SKILL.md": manifest, "z-last.md": "Last.", "a/nested.md": "Nested.", "a-first.md": "First.",
		"nested/" + compatibilitySkillDraftMarker: "Ordinary nested author resource.",
		"catalog-ui.json":                         `{"schema_version":1,"skills":{"package-inventory-fixture":{"category":"workflow","description_i18n":{"en":"Package fixture."}}}}`,
	} {
		if _, err := fixture.call("host.skills.edit", name, path, content); err != nil {
			t.Fatal(err)
		}
	}
	published, err := fixture.call("host.skills.publish", name, false)
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(mapValue(published)["skill_id"])
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Draft package instructions.", "Original package instructions."); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "draft-only.md", "Draft-only resource."); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{id, name} {
		t.Run(reference, func(t *testing.T) {
			result, err := fixture.call("host.skills.read", reference)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"SKILL.md", "a-first.md", "a/nested.md", "catalog-ui.json", "nested/" + compatibilitySkillDraftMarker, "z-last.md"}
			content := manifest
			if reference == name {
				want = []string{"SKILL.md", "a-first.md", "a/nested.md", "catalog-ui.json", "draft-only.md", "nested/" + compatibilitySkillDraftMarker, "z-last.md"}
				content = strings.Replace(manifest, "Original package instructions.", "Draft package instructions.", 1)
			}
			assertKernelSkillInventory(t, result, want)
			if stringValue(mapValue(result)["content"]) != content {
				t.Fatalf("inventory and content did not select the same package: %#v", result)
			}
			resource, err := fixture.call("host.skills.read", reference, "a/nested.md")
			if err != nil || stringValue(mapValue(resource)["content"]) != "Nested." {
				t.Fatalf("resource read=%#v err=%v", resource, err)
			}
			if _, found := mapValue(resource)["files"]; found {
				t.Fatalf("non-manifest read exposed an inventory: %#v", resource)
			}
			nested, err := fixture.call("host.skills.read", reference, "nested/"+compatibilitySkillDraftMarker)
			if err != nil || stringValue(mapValue(nested)["content"]) != "Ordinary nested author resource." {
				t.Fatalf("ordinary nested author resource was classified as a root control: %#v err=%v", nested, err)
			}
		})
	}
}

func TestKernelSkillReadNormalizesResourcePathOnce(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "normalized-inventory-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Repairable draft without frontmatter."); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Guide."); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", " ", "./SKILL.md", "./references/guide.md"} {
		t.Run(path, func(t *testing.T) {
			result, err := fixture.server.kernelHostReadSkill(name, path)
			if err != nil {
				t.Fatal(err)
			}
			want := "SKILL.md"
			if strings.Contains(path, "references/") {
				want = "references/guide.md"
			}
			if mapValue(result)["path"] != want {
				t.Errorf("response path=%#v want=%q", mapValue(result)["path"], want)
			}
			_, hasFiles := mapValue(result)["files"]
			if hasFiles != (want == "SKILL.md") {
				t.Errorf("manifest-only inventory mismatch: %#v", result)
			}
		})
	}
}

func TestKernelSkillReadBuiltinManifestInventory(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "builtin-inventory-fixture"
	fixture.server.skillCatalog.AddSkill(skills.Skill{Name: name, Path: "builtin:" + name, Body: "Actual built-in instructions."})
	result, err := fixture.call("host.skills.read", "bundled:"+name)
	if err != nil {
		t.Fatal(err)
	}
	assertKernelSkillInventory(t, result, []string{"SKILL.md"})
	if stringValue(mapValue(result)["content"]) != "Actual built-in instructions." {
		t.Fatalf("built-in content was synthesized: %#v", result)
	}
	if result, err := fixture.call("host.skills.read", "bundled:"+name, "assets/missing.bin"); err == nil || result != nil {
		t.Fatalf("built-in accepted a nonexistent resource: %#v err=%v", result, err)
	}
}

func TestKernelSkillReadBareCatalogRejectsPendingPublication(t *testing.T) {
	for _, marker := range []string{skillImportPendingMarker, skillImportPrivateMarker} {
		t.Run(marker, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "pending-inventory-fixture"
			if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(kernelSkillManifestForTest(name, "Published package."))); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(fixture.server.fileRoot, "skills", name)
			if err := os.WriteFile(filepath.Join(root, marker), []byte("private publication control"), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.call("host.skills.read", name)
			if err == nil || result != nil || strings.Contains(fmt.Sprint(err), fixture.server.fileRoot) {
				t.Fatalf("bare catalog read bypassed publication fence or leaked a root: %#v err=%v", result, err)
			}
		})
	}
}

func TestKernelSkillReadPrivateControlResourcesCannotBeRead(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "private-inventory-fixture"
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Repairable draft without frontmatter.\n"); err != nil {
		t.Fatal(err)
	}
	draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
	if err != nil || !found {
		t.Fatalf("draft found=%t err=%v", found, err)
	}
	root := filepath.Dir(draft.Path)
	for _, marker := range []string{compatibilitySkillDraftMarker, skillImportPrivateMarker, skillImportPendingMarker} {
		if err := os.WriteFile(filepath.Join(root, marker), []byte("private control"), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{marker, "./" + marker, strings.ToUpper(marker)} {
			result, err := fixture.call("host.skills.read", name, path)
			if err == nil || result != nil || strings.Contains(fmt.Sprint(err), fixture.server.fileRoot) {
				t.Errorf("control resource %q was exposed: %#v err=%v", path, result, err)
			}
		}
	}
	result, err := fixture.call("host.skills.read", name)
	if err != nil {
		t.Fatal(err)
	}
	assertKernelSkillInventory(t, result, []string{"SKILL.md"})
	if stringValue(mapValue(result)["content"]) != "Repairable draft without frontmatter.\n" {
		t.Fatalf("inventory made publishability a read prerequisite: %#v", result)
	}
}

func TestKernelSkillReadManifestInventoryRefusesUnsafePackages(t *testing.T) {
	for _, unsafe := range []string{"symlink", "hardlink", "depth", "files", "bytes"} {
		t.Run(unsafe, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "unsafe-inventory-fixture"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Safe instructions.")); err != nil {
				t.Fatal(err)
			}
			draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
			if err != nil || !found {
				t.Fatalf("draft found=%t err=%v", found, err)
			}
			root := filepath.Dir(draft.Path)
			switch unsafe {
			case "symlink", "hardlink":
				outside := filepath.Join(t.TempDir(), "outside.txt")
				if err := os.WriteFile(outside, []byte("External bytes."), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(root, "unsafe.txt")
				if unsafe == "symlink" {
					err = os.Symlink(outside, link)
				} else {
					err = os.Link(outside, link)
				}
				if err != nil {
					t.Skipf("link fixture unavailable: %v", err)
				}
			case "depth":
				if err := os.MkdirAll(filepath.Join(root, strings.Repeat("nested/", 9)), 0o700); err != nil {
					t.Fatal(err)
				}
			case "files":
				for index := 0; index < maxSkillArchiveFiles; index++ {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("resource-%04d.txt", index)), []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "bytes":
				file, err := os.Create(filepath.Join(root, "large.bin"))
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := file.Truncate(maxSkillArchiveBytes + 1)
				closeErr := file.Close()
				if truncateErr != nil || closeErr != nil {
					t.Fatalf("sparse byte fixture: %v / %v", truncateErr, closeErr)
				}
			}
			result, err := fixture.call("host.skills.read", name)
			if err == nil || result != nil || strings.Contains(fmt.Sprint(err), fixture.server.fileRoot) {
				t.Fatalf("unsafe package yielded content/partial inventory or leaked a root: %#v err=%v", result, err)
			}
		})
	}
}

func assertKernelSkillInventory(t *testing.T, result any, want []string) {
	t.Helper()
	files, ok := mapValue(result)["files"].([]string)
	if !ok || !reflect.DeepEqual(files, want) {
		t.Errorf("inventory=%#v want=%#v", mapValue(result)["files"], want)
	}
	for _, path := range files {
		if filepath.IsAbs(path) || strings.Contains(path, "\\") || !filepath.IsLocal(filepath.FromSlash(path)) {
			t.Errorf("inventory contains a non-relative package path: %q", path)
		}
	}
}
