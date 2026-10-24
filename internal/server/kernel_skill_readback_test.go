package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"x-science/internal/skills"
)

func TestKernelSkillReadPublishedReceiptKeepsNamespaceAndDraftIsolation(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "published-readback-fixture"
	manifest := kernelSkillManifestForTest(name, "Published workflow.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Published reference."); err != nil {
		t.Fatal(err)
	}
	published, err := fixture.publishReviewed(name, false)
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(mapValue(published)["skill_id"])
	if id != "local:"+name {
		t.Fatalf("unexpected publication identity: %#v", published)
	}
	assertKernelSkillReadback(t, fixture, id, "SKILL.md", manifest)
	assertKernelSkillReadback(t, fixture, id, "references/guide.md", "Published reference.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Unpublished revision.", "Published workflow."); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Unpublished reference.", "Published reference."); err != nil {
		t.Fatal(err)
	}
	// An explicit publication identity never falls through to the same-name draft.
	assertKernelSkillReadback(t, fixture, id, "SKILL.md", manifest)
	assertKernelSkillReadback(t, fixture, id, "references/guide.md", "Published reference.")
	assertKernelSkillReadback(t, fixture, name, "SKILL.md", strings.Replace(manifest, "Published workflow.", "Unpublished revision.", 1))
	assertKernelSkillReadback(t, fixture, name, "references/guide.md", "Unpublished reference.")
}

func TestKernelSkillReadOriginFollowsSelectedPublicationSnapshot(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "origin-readback-fixture"
	manifest := kernelSkillManifestForTest(name, "Published origin workflow.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Published origin reference."); err != nil {
		t.Fatal(err)
	}
	assertOrigin := func(reference, path, wantOrigin, wantContent string) {
		t.Helper()
		result, err := fixture.call("host.skills.read", reference, path)
		if err != nil {
			t.Fatal(err)
		}
		read := mapValue(result)
		if read["origin"] != wantOrigin || read["name"] != name || read["path"] != path || read["content"] != wantContent {
			t.Errorf("read %q/%q returned %#v; want origin=%q and selected package content", reference, path, read, wantOrigin)
		}
		if path == "SKILL.md" && len(anySliceValue(read["files"])) == 0 {
			t.Fatalf("manifest origin read lost its package inventory: %#v", read)
		}
	}
	assertOrigin(name, "SKILL.md", "draft", manifest)
	assertOrigin(name, "references/guide.md", "draft", "Published origin reference.")
	published, err := fixture.publishReviewed(name, false)
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(mapValue(published)["skill_id"])
	if id != "local:"+name {
		t.Fatalf("unexpected publication identity: %#v", published)
	}
	assertOrigin(id, "SKILL.md", "personal", manifest)
	assertOrigin(id, "references/guide.md", "personal", "Published origin reference.")
	assertOrigin(name, "SKILL.md", "personal", manifest)
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", "Draft origin revision.", "Published origin workflow."); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.call("host.skills.edit", name, "references/guide.md", "Draft origin reference.", "Published origin reference."); err != nil {
		t.Fatal(err)
	}
	// Origin follows the package actually read, not the bare name or the presence
	// of a same-name unpublished draft beside a selected publication.
	assertOrigin(name, "SKILL.md", "draft", strings.Replace(manifest, "Published origin workflow.", "Draft origin revision.", 1))
	assertOrigin(name, "references/guide.md", "draft", "Draft origin reference.")
	assertOrigin(id, "SKILL.md", "personal", manifest)
	assertOrigin(id, "references/guide.md", "personal", "Published origin reference.")
	const builtin = "builtin-origin-fixture"
	fixture.server.skillCatalog.AddSkill(skills.Skill{Name: builtin, Path: "builtin:" + builtin, Body: "Built-in origin workflow."})
	for _, reference := range []string{builtin, "bundled:" + builtin} {
		result, err := fixture.call("host.skills.read", reference, "SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		read := mapValue(result)
		if read["origin"] != "x_science_llm" || read["content"] != "Built-in origin workflow." {
			t.Errorf("built-in origin does not match the selected catalog source: %#v", read)
		}
	}
}

func TestKernelSkillReadPublishedReferenceRejectsWrongNamespaceAndUnsafeResource(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const personal, builtin = "personal-readback-fixture", "builtin-readback-fixture"
	if _, err := fixture.server.importSkillBundle(personal+".md", "", false,
		[]byte(kernelSkillManifestForTest(personal, "Published personal workflow."))); err != nil {
		t.Fatal(err)
	}
	fixture.server.skillCatalog.AddSkill(skills.Skill{Name: builtin, Path: "builtin:" + builtin, Body: "Built-in workflow."})
	assertKernelSkillReadback(t, fixture, "bundled:"+builtin, "SKILL.md", "Built-in workflow.")
	assertKernelSkillReadback(t, fixture, builtin, "SKILL.md", "Built-in workflow.")
	for _, reference := range []string{"bundled:" + personal, "local:" + builtin, "outside:" + personal,
		"local:", "local:../escape", "local:" + personal + ":extra", "local:missing-fixture"} {
		t.Run(reference, func(t *testing.T) {
			if result, err := fixture.call("host.skills.read", reference); err == nil {
				t.Fatalf("reference crossed the declared source namespace: %#v", result)
			}
		})
	}
	for _, path := range []string{"../outside.md", "/etc/passwd", "references/../../outside.md", "references/missing.md"} {
		t.Run(path, func(t *testing.T) {
			if result, err := fixture.call("host.skills.read", "local:"+personal, path); err == nil {
				t.Fatalf("published read bypassed resource confinement: %#v", result)
			}
		})
	}
	if result, err := fixture.call("host.skills.read", "bundled:"+builtin, "references/guide.md"); err == nil {
		t.Fatalf("built-in read accepted a nonexistent resource: %#v", result)
	}
	t.Run("symlink-resource", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("Outside data must not be read."), 0o600); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(fixture.server.fileRoot, "skills", personal)
		if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		if result, err := fixture.call("host.skills.read", "local:"+personal, "escape.md"); err == nil {
			t.Fatalf("published read followed a resource symlink: %#v", result)
		}
	})
	const draftOnly = "unpublished-readback-fixture"
	if _, err := fixture.call("host.skills.edit", draftOnly, "SKILL.md", kernelSkillManifestForTest(draftOnly, "Draft only.")); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.call("host.skills.read", "local:"+draftOnly); err == nil {
		t.Fatalf("draft was exposed as published: %#v", result)
	}
}

func TestKernelSkillReadPublishedReferenceRejectsPendingPublication(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "pending-readback-fixture"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false,
		[]byte(kernelSkillManifestForTest(name, "Confirmed original workflow."))); err != nil {
		t.Fatal(err)
	}
	current, found := findCatalogSkill(fixture.server.skillCatalog, name)
	if !found {
		t.Fatal("published fixture not in the catalog")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(current.Path), compatibilitySkillDraftMarker), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.call("host.skills.read", "local:"+name); err == nil {
		t.Fatalf("pending marker was bypassed by explicit published identity: %#v", result)
	}
}

func TestKernelSkillReadPublishedReferenceUsesManagedDirectory(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	// This valid personal name overlaps an existing bundled presentation path
	// segment. Source authority comes from the managed directory, not the label.
	const name = "x-science"
	manifest := kernelSkillManifestForTest(name, "User-owned published workflow.")
	if _, err := fixture.call("host.skills.edit", name, "SKILL.md", manifest); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.publishReviewed(name, false)
	if err != nil {
		t.Fatal(err)
	}
	assertKernelSkillReadback(t, fixture, stringValue(mapValue(result)["skill_id"]), "SKILL.md", manifest)
	if value, err := fixture.call("host.skills.read", "bundled:"+name); err == nil {
		t.Fatalf("personal package crossed into the bundled namespace: %#v", value)
	}
}

func assertKernelSkillReadback(t *testing.T, fixture *kernelSkillDraftFixture, reference, path, want string) {
	t.Helper()
	result, err := fixture.call("host.skills.read", reference, path)
	if err != nil || stringValue(mapValue(result)["content"]) != want {
		t.Fatalf("read %q/%q returned %#v, err=%v; want exact package bytes", reference, path, result, err)
	}
}
