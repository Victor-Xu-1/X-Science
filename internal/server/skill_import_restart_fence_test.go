package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestPreparedSkillImportCannotReenterRestartCatalog(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "prepared-not-published"
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Unpublished prepared instructions.", false)
	if _, err := os.Stat(prepared.skill.Path); err != nil {
		t.Fatal(err)
	}
	for _, configured := range [][]string{nil, {personalSkillDirectory(fixture.server.fileRoot)}} {
		assertUnpublishedImportAbsentFromRestart(t, fixture.server, configured, prepared.staging, name)
	}
}

func TestRetainedSkillImportFailureCannotReenterRestartCatalog(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	prepared := prepareSkillPublicationTest(t, fixture.server, "retained-publication", "Package awaiting metadata.", false)
	const retainedName = "retained-unpublished-recovery"
	_, _, err := fixture.server.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			// The real journal cannot move its destination back over another
			// staging owner, so this failure must preserve that staging tree.
			if err := os.Mkdir(prepared.sourceRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(prepared.sourceRoot, "SKILL.md"),
				[]byte(kernelSkillManifestForTest(retainedName, "Retained but never authorized instructions.")), 0o600); err != nil {
				t.Fatal(err)
			}
			return errors.New("metadata write failed while staging ownership changed")
		})
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != "outcome_unknown" || !prepared.retain {
		t.Fatalf("did not reach actual retained rollback: retain=%t err=%v", prepared.retain, err)
	}
	prepared.cleanup()
	if _, err := os.Stat(filepath.Join(prepared.sourceRoot, "SKILL.md")); err != nil {
		t.Fatalf("recovery evidence was not retained: %v", err)
	}
	assertUnpublishedImportAbsentFromRestart(t, fixture.server, nil, prepared.staging, retainedName)
	assertUnpublishedImportAbsentFromRestart(t, fixture.server, nil,
		filepath.Join(fixture.server.fileRoot, "skills", "retained-publication"), "retained-publication")
}

func TestSkillReplacementBackupCannotReenterRestartCatalog(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	const name = "replacement-backup-restart"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false,
		[]byte(kernelSkillManifestForTest(name, "Previous installed instructions."))); err != nil {
		t.Fatal(err)
	}
	prepared := prepareSkillPublicationTest(t, fixture.server, name, "Current installed instructions.", true)
	destination := filepath.Join(fixture.server.fileRoot, "skills", name)
	fixture.server.skillMutationMu.Lock()
	entry, err := stageSkillDirectoryPublication(prepared.filesystem, prepared.sourceRoot, destination, true)
	fixture.server.skillMutationMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if entry.backup == "" {
		t.Fatal("replacement did not retain its actual backup")
	}
	if _, err := os.Stat(filepath.Join(entry.backup, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	// Leave the real backup in place as a failed cleanup/restart would.
	catalog, _, failures := loadSkillCatalog(nil, nil, fixture.server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("restart catalog failures=%#v", failures)
	}
	count := 0
	for _, skill := range catalog.Skills() {
		if pathWithinRoot(skill.Path, entry.backup) {
			t.Errorf("replacement backup entered executable catalog: %s", skill.Path)
		}
		if strings.EqualFold(skill.Name, name) {
			count++
			if !sameCleanPath(skill.Path, filepath.Join(destination, "SKILL.md")) ||
				!strings.Contains(skill.Body, "Current installed instructions.") {
				t.Errorf("restart selected stale replacement backup: path=%s body=%q", skill.Path, skill.Body)
			}
		}
	}
	if count != 1 {
		t.Errorf("restart must expose exactly the live replacement, got %d matching entries", count)
	}
}

func assertUnpublishedImportAbsentFromRestart(t *testing.T, server *Server, configured []string, managedRoot, name string) {
	t.Helper()
	catalog, _, failures := loadSkillCatalog(nil, configured, server.fileRoot)
	if len(failures) != 0 {
		t.Fatalf("restart catalog failures=%#v", failures)
	}
	for _, skill := range catalog.Skills() {
		if pathWithinRoot(skill.Path, managedRoot) || strings.EqualFold(skill.Name, name) {
			t.Errorf("unpublished import entered executable catalog: name=%s path=%s", skill.Name, skill.Path)
		}
	}
	previous := server.skillCatalog
	server.skillCatalog = catalog
	defer func() { server.skillCatalog = previous }()
	if result, err := server.executeSkillTool(map[string]any{"skill": name}); err == nil {
		t.Errorf("restart admitted unpublished Skill instructions: %#v", result)
	}
}
