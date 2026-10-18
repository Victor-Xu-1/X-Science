package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestSkillImportReplacementRollbackRestoresOriginalBeforeUnlock(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "replacement-rollback"
	original := kernelSkillManifestForTest(name, "Original user content.")
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(original)); err != nil {
		t.Fatal(err)
	}
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Replacement content.", true)
	injected := errors.New("metadata write failed")
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			if fixture.server.skillMutationMu.TryLock() {
				fixture.server.skillMutationMu.Unlock()
				t.Fatal("publication mutex released before metadata and compensation")
			}
			raw, err := os.ReadFile(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md"))
			if err != nil || !bytes.Contains(raw, []byte("Replacement content.")) {
				t.Fatalf("replacement was not actually promoted before metadata failure: %q err=%v", raw, err)
			}
			return injected
		})
	if !errors.Is(err, injected) {
		t.Fatalf("metadata failure=%v", err)
	}
	if !fixture.server.skillMutationMu.TryLock() {
		t.Fatal("publication mutex remained locked")
	}
	fixture.server.skillMutationMu.Unlock()
	raw, err := os.ReadFile(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md"))
	if err != nil || string(raw) != original {
		t.Fatalf("rollback lost original package: %q err=%v", raw, err)
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); !found {
		t.Fatal("rollback removed original catalog entry")
	}
}

func TestSkillImportRollbackPreservesUnknownDestinationAndStagingOwnership(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "unknown-publication-owner"
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Original publication bytes.", false)
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	retained := destination + ".retained-original"
	retainedStaging := prepared.staging + "-retained"
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			if err := os.Rename(destination, retained); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, "foreign.txt"), []byte("new owner"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Recreate even the staging path as another owner. cleanup must not
			// undo the journal's ownership refusal through a deferred RemoveAll.
			if _, err := os.Stat(prepared.staging); err == nil {
				if err := os.Rename(prepared.staging, retainedStaging); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(prepared.staging, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(prepared.staging, "foreign.txt"), []byte("new staging owner"), 0o600); err != nil {
				t.Fatal(err)
			}
			return errors.New("metadata unavailable after external directory replacement")
		})
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" {
		t.Fatalf("ownership loss was not explicit: %v", err)
	}
	prepared.cleanup()
	for _, path := range []string{
		filepath.Join(destination, "foreign.txt"), filepath.Join(prepared.staging, "foreign.txt"),
		filepath.Join(retained, "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("unverified or retained resource was deleted: %s: %v", path, err)
		}
	}
	if _, found := findCatalogSkill(fixture.server.skillCatalog, name); found {
		t.Fatal("metadata failure published a catalog entry")
	}
}

func TestSkillImportBatchCompensatesEarlierPublicationWhenLaterIOFails(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	first := prepareSkillPublicationTest(t, fixture.server, "batch-first", "First package.", false)
	second := prepareSkillPublicationTest(t, fixture.server, "batch-second", "Second package.", false)
	if err := os.RemoveAll(second.sourceRoot); err != nil {
		t.Fatal(err)
	}
	recorded := false
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{first, second}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			recorded = true
			return nil
		})
	if err == nil || recorded {
		t.Fatalf("partial IO failure=%v metadata callback=%t", err, recorded)
	}
	for _, item := range []*preparedSkillImport{first, second} {
		if _, err := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", item.skill.Name)); !os.IsNotExist(err) {
			t.Errorf("partial publication remained for %s: %v", item.skill.Name, err)
		}
		if _, found := findCatalogSkill(fixture.server.skillCatalog, item.skill.Name); found {
			t.Errorf("partial catalog entry remained for %s", item.skill.Name)
		}
	}
}

func TestSkillImportStagingCleanupRejectsReplacedRoot(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	prepared := prepareSkillPublicationTest(t, fixture.server, "cleanup-identity", "Prepared package.", false)
	if err := os.Rename(prepared.staging, prepared.staging+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(prepared.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(prepared.staging, "new-owner.txt")
	if err := os.WriteFile(marker, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared.cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup removed a different staging owner: %v", err)
	}
}

func prepareSkillPublicationTest(t *testing.T, server *Server, name, body string, replace bool) *preparedSkillImport {
	t.Helper()
	prepared, err := server.prepareSkillBundle(name+".md", "", replace, []byte(kernelSkillManifestForTest(name, body)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prepared.cleanup)
	return prepared
}
