package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestSkillImportPendingReplacementRemainsUnavailableUntilMetadataCommits(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "pending-replacement-visibility"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false,
		[]byte(kernelSkillManifestForTest(name, "Old authorized instructions."))); err != nil {
		t.Fatal(err)
	}
	const consumer = "pending-replacement-consumer"
	manifest := "---\nname: " + consumer + "\ndescription: Consumer of the replaced Skill.\nmetadata:\n  dependencies:\n    skills: [" + name + "]\n---\n\nUse the declared dependency.\n"
	if _, err := fixture.server.importSkillBundle(consumer+".md", "", false, []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Confirmed replacement instructions.", true)
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			// The cached old catalog has the same final path. It cannot use the
			// newly promoted package before its metadata has committed.
			for _, requested := range []string{name, consumer} {
				if result, err := fixture.server.executeSkillTool(map[string]any{"skill": requested}); err == nil {
					t.Errorf("cached catalog invoked pending replacement through %s: %#v", requested, result)
				}
			}
			assertUnpublishedImportAbsentFromRestart(t, fixture.server, nil, destination, name)
			return fixture.server.recordSkillImports(fixture.bound.access.UserID, nil, []webSkillImportHistoryEntry{
				newWebSkillImportHistory("visibility-confirmed", "File upload", "", name+".md", name, "imported", ""),
			})
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, skillImportPendingMarker)); !os.IsNotExist(err) {
		t.Fatalf("confirmed replacement still has pending visibility: %v", err)
	}
	catalog, _, failures := loadSkillCatalog(nil, nil, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("confirmed catalog failures=%#v", failures)
	}
	skill, found := findCatalogSkill(catalog, name)
	if !found || !strings.Contains(skill.Body, "Confirmed replacement instructions.") {
		t.Fatalf("confirmed replacement absent after restart: %#v", skill)
	}
	fixture.server.skillCatalog = catalog
	for _, requested := range []string{name, consumer} {
		if _, err := fixture.server.executeSkillTool(map[string]any{"skill": requested}); err != nil {
			t.Fatalf("confirmed replacement cannot be invoked through %s: %v", requested, err)
		}
	}
}

func TestSkillImportVisibilityConfirmationFailureRetainsUnpublishedResources(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "visibility-confirmation-unknown"
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Resources needing visibility confirmation.", false)
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			if err := fixture.server.recordSkillImports(fixture.bound.access.UserID, nil, []webSkillImportHistoryEntry{
				newWebSkillImportHistory("visibility-unknown", "File upload", "", name+".md", name, "imported", ""),
			}); err != nil {
				return err
			}
			marker := filepath.Join(destination, skillImportPendingMarker)
			if err := os.Rename(marker, marker+".retained-original"); err != nil {
				t.Fatal(err)
			}
			if _, err := prepared.filesystem.createMarker(destination, skillImportPendingMarker); err != nil {
				t.Fatal(err)
			}
			return nil
		})
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" || !prepared.retain {
		t.Fatalf("visibility confirmation was not unknown: retain=%t err=%v", prepared.retain, err)
	}
	prepared.cleanup()
	if _, err := os.Stat(filepath.Join(destination, "SKILL.md")); err != nil {
		t.Fatalf("uncertain publication was deleted: %v", err)
	}
	history, err := fixture.server.loadWebSkillImportHistory(fixture.bound.access.UserID)
	if err != nil || len(history) != 1 {
		t.Fatalf("already committed metadata was removed: %#v err=%v", history, err)
	}
	assertUnpublishedImportAbsentFromRestart(t, fixture.server, nil, destination, name)
}

func TestSkillImportVisibilityPreservesLegitimateBackupNamesAndHiddenSources(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const named = "ordinary.backup-01234567-89ab-cdef-0123-456789abcdef"
	if _, err := fixture.server.importSkillBundle(named+".md", "", false,
		[]byte(kernelSkillManifestForTest(named, "Legitimate user Skill name."))); err != nil {
		t.Fatal(err)
	}
	const hidden = "ordinary-hidden-source"
	directory := filepath.Join(fixture.server.fileRoot, "skills", ".user-library")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"),
		[]byte(kernelSkillManifestForTest(hidden, "Explicit personal hidden directory, without an import control marker.")), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, _, failures := loadSkillCatalog(nil, []string{personalSkillDirectory(fixture.server.fileRoot)}, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("legitimate catalog failures=%#v", failures)
	}
	fixture.server.skillCatalog = catalog
	for _, name := range []string{named, hidden} {
		if _, found := findCatalogSkill(catalog, name); !found {
			t.Errorf("legitimate Skill was hidden by a filename heuristic: %s", name)
		}
		if _, err := fixture.server.executeSkillTool(map[string]any{"skill": name}); err != nil {
			t.Errorf("legitimate Skill cannot be invoked: %s: %v", name, err)
		}
	}
}

func TestSkillImportRejectsBundledVisibilityControlMarkers(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	for _, marker := range []string{skillImportPrivateMarker, skillImportPendingMarker} {
		raw := buildSkillArchive(t, map[string]string{
			"package/SKILL.md":  kernelSkillManifestForTest("reserved-import-control", "Not an authority field."),
			"package/" + marker: "forged visibility marker",
		})
		prepared, err := fixture.server.prepareSkillBundle("reserved-import-control.zip", "", false, raw)
		if prepared != nil {
			prepared.cleanup()
		}
		if err == nil {
			t.Errorf("untrusted bundle supplied product visibility control: %s", marker)
		}
	}
}

func TestSkillImportCannotReplaceUnresolvedPublication(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "preserve-unresolved-publication"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false,
		[]byte(kernelSkillManifestForTest(name, "Retained original publication."))); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	filesystem, err := openSkillImportFilesystem(fixture.server.fileRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	defer filesystem.close()
	if _, err := filesystem.createMarker(destination, skillImportPendingMarker); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.server.importSkillBundle(name+".md", "", true,
		[]byte(kernelSkillManifestForTest(name, "Must not erase recovery resources.")))
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" {
		t.Fatalf("replacement did not preserve unresolved publication: %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(destination, "SKILL.md"))
	if readErr != nil || !strings.Contains(string(raw), "Retained original publication.") {
		t.Fatalf("replacement erased recovery resources: %q err=%v", raw, readErr)
	}
	assertUnpublishedImportAbsentFromRestart(t, fixture.server, nil, destination, name)
}
