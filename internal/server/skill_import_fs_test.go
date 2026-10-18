package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestSkillImportPublicationRejectsReboundRootWithoutOutsideWrites(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "root-capability-rebind"
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Authorized prepared bytes.", false)
	outside := t.TempDir()
	relative, err := filepath.Rel(prepared.filesystem.path, prepared.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	outsidePackage := filepath.Join(outside, relative)
	if err := os.MkdirAll(outsidePackage, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsidePackage, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside must not move"), 0o600); err != nil {
		t.Fatal(err)
	}
	installRoot := prepared.filesystem.path
	movedRoot := installRoot + "-original"
	if err := os.Rename(installRoot, movedRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, installRoot); err != nil {
		_ = os.Rename(movedRoot, installRoot)
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(installRoot)
		_ = os.Rename(movedRoot, installRoot)
	}()
	recorded := false
	_, _, err = fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			recorded = true
			return nil
		})
	if err == nil || recorded {
		t.Fatalf("rebound root admitted publication: err=%v recorded=%t", err, recorded)
	}
	prepared.cleanup()
	raw, err := os.ReadFile(outsideFile)
	if err != nil || string(raw) != "outside must not move" {
		t.Fatalf("publication or cleanup followed rebound root: %q err=%v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(outside, name)); !os.IsNotExist(err) {
		t.Fatalf("outside publication was created: %v", err)
	}
}

func TestSkillImportRootCapabilityRejectsParentAndSymlinkEscape(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	prepared := prepareSkillPublicationTest(t, fixture.server, "root-escape-source", "Authorized source.", false)
	filesystem := prepared.filesystem
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("untouched external content"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filesystem.path, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{outside, link} {
		if _, err := filesystem.createMarker(destination, skillImportPendingMarker); err == nil {
			t.Errorf("marker write escaped through %s", destination)
		}
		if err := filesystem.rename(prepared.sourceRoot, filepath.Join(destination, "moved")); err == nil {
			t.Errorf("rename escaped through %s", destination)
		}
		if err := filesystem.remove(filepath.Join(destination, "victim.txt"), true); err == nil {
			t.Errorf("cleanup escaped through %s", destination)
		}
		if _, err := filesystem.lstat(filepath.Join(destination, "victim.txt")); err == nil {
			t.Errorf("inspection escaped through %s", destination)
		}
	}
	raw, err := os.ReadFile(victim)
	if err != nil || string(raw) != "untouched external content" {
		t.Fatalf("outside victim changed: %q err=%v", raw, err)
	}
	if _, err := os.Stat(prepared.skill.Path); err != nil {
		t.Fatalf("escape attempt moved authorized source: %v", err)
	}
}

func TestSkillImportRollbackRejectsReboundStagingAncestor(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "rooted-rollback-retention"
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Retain on unsafe rollback.", false)
	drafts := filepath.Join(prepared.filesystem.path, compatibilitySkillDraftDirectory)
	movedDrafts := drafts + "-original"
	outside := t.TempDir()
	relative, err := filepath.Rel(drafts, prepared.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	outsideTarget := filepath.Join(outside, relative)
	if err := os.MkdirAll(filepath.Dir(outsideTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	rebound := false
	defer func() {
		if rebound {
			_ = os.Remove(drafts)
			_ = os.Rename(movedDrafts, drafts)
		}
	}()
	_, _, err = fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			if err := os.Rename(drafts, movedDrafts); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, drafts); err != nil {
				_ = os.Rename(movedDrafts, drafts)
				t.Fatal(err)
			}
			rebound = true
			return errors.New("metadata failed after staging ancestor replacement")
		})
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" || !prepared.retain {
		t.Fatalf("unsafe rollback did not retain resources: retain=%t err=%v", prepared.retain, err)
	}
	if _, err := os.Stat(outsideTarget); !os.IsNotExist(err) {
		t.Fatalf("rollback moved publication outside the capability: %v", err)
	}
	destination := filepath.Join(prepared.filesystem.path, name)
	if _, err := os.Stat(filepath.Join(destination, "SKILL.md")); err != nil {
		t.Fatalf("unsafe rollback deleted publication evidence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, skillImportPendingMarker)); err != nil {
		t.Fatalf("unsafe rollback removed pending visibility: %v", err)
	}
}

func TestSkillImportVisibilityAcceptsRootManifestAndExternalReadOnlySource(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	root := filepath.Join(fixture.server.fileRoot, "skills")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"),
		[]byte(kernelSkillManifestForTest("personal-root-manifest", "Legitimate root manifest.")), 0o600); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "SKILL.md"),
		[]byte(kernelSkillManifestForTest("external-readonly-manifest", "Legitimate explicit external source.")), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, _, failures := loadSkillCatalog(nil, []string{root, external}, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("explicit catalog failures=%#v", failures)
	}
	fixture.server.skillCatalog = catalog
	for _, name := range []string{"personal-root-manifest", "external-readonly-manifest"} {
		skill, found := findCatalogSkill(catalog, name)
		if !found || !strings.Contains(skill.Body, "Legitimate") {
			t.Errorf("legitimate source excluded: %s", name)
		}
		if _, err := fixture.server.executeSkillTool(map[string]any{"skill": name}); err != nil {
			t.Errorf("legitimate source could not be invoked: %s: %v", name, err)
		}
	}
}

func TestSkillImportPrivateDirectoryCleanupPreservesReplacement(t *testing.T) {
	filesystem, err := openSkillImportFilesystem(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer filesystem.close()
	directory, identity, err := filesystem.createPrivateDirectory()
	if err != nil {
		t.Fatal(err)
	}
	retained := directory + "-retained"
	if err := os.Rename(directory, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	replacementFile := filepath.Join(directory, "replacement.txt")
	if err := os.WriteFile(replacementFile, []byte("not owned by failed initialization"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.removeOwnedDirectory(directory, identity); err == nil {
		t.Fatal("cleanup accepted a replaced private directory")
	}
	raw, err := os.ReadFile(replacementFile)
	if err != nil || string(raw) != "not owned by failed initialization" {
		t.Fatalf("cleanup removed the replacement: %q err=%v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(retained, skillImportPrivateMarker)); err != nil {
		t.Fatalf("cleanup removed retained original evidence: %v", err)
	}
	if err := filesystem.removeOwnedDirectory(retained, identity); err != nil {
		t.Fatalf("exact owned cleanup failed: %v", err)
	}
	if _, err := os.Stat(retained); !os.IsNotExist(err) {
		t.Fatalf("exact owned directory survived successful cleanup: %v", err)
	}
}
