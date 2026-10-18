package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"x-science/internal/skills"
)

func TestSelectedSkillDependenciesAndRestoredClosureRejectPendingPublication(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	app := fixture.server
	const dependency = "selected-pending-dependency"
	if _, err := app.importSkillBundle(dependency+".md", "", false,
		[]byte(kernelSkillManifestForTest(dependency, "Original dependency."))); err != nil {
		t.Fatal(err)
	}
	consumer := skills.Skill{Name: "selected-dependency-consumer", Path: "builtin:selected-dependency-consumer",
		Body: "Follow the declared dependency.", RequiredSkills: []string{dependency}}
	app.skillCatalog.AddSkill(consumer)
	cached, err := app.expandRuntimeSkillDependencies([]skills.Skill{consumer}, nil, nil, false, nil)
	if err != nil || len(cached) != 2 {
		t.Fatalf("original closure=%#v err=%v", cached, err)
	}
	prepared := prepareSkillPublicationTest(t, app, dependency, "Replacement dependency.", true)
	_, _, err = app.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			_, err := app.expandRuntimeSkillDependencies([]skills.Skill{consumer}, nil, nil, false, nil)
			assertPendingSelectedSkillRejected(t, nil, err)
			restored, err := app.prepareAgentSkillRuntimeContexts(context.Background(), "unused-inline-session", cached)
			assertPendingSelectedSkillRejected(t, restored, err)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := app.expandRuntimeSkillDependencies([]skills.Skill{consumer}, nil, nil, false, nil)
	if err != nil || len(confirmed) != 2 || !strings.Contains(confirmed[0].Body, "Replacement dependency.") {
		t.Fatalf("confirmed dependency closure=%#v err=%v", confirmed, err)
	}
}

func TestSelectedSkillPreparationRejectsPrivatePreparedPackage(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	prepared := prepareSkillPublicationTest(t, fixture.server, "private-selected-package", "Not published.", false)
	private := prepared.skill
	fixture.server.skillCatalog.AddSkill(private)
	_, err := fixture.server.runtimeSkillsByName([]string{private.Name}, nil)
	assertPendingSelectedSkillRejected(t, nil, err)
	_, err = fixture.server.expandRuntimeSkillDependencies([]skills.Skill{private}, nil, nil, false, nil)
	assertPendingSelectedSkillRejected(t, nil, err)
	selected, err := fixture.server.prepareAgentSkillRuntimeContexts(context.Background(), "unused-inline-session", []skills.Skill{private})
	assertPendingSelectedSkillRejected(t, selected, err)
}

func TestRuntimeSkillSnapshotSerializesPublicationAndRejectsChangedCachedContract(t *testing.T) {
	fixture := newKernelSkillMarketplaceInstallFixture(t)
	app := fixture.server
	const name = "publication-snapshot-contract"
	archive := func(body, resource string) []byte {
		return buildSkillArchive(t, map[string]string{
			name + "/SKILL.md":              kernelSkillManifestForTest(name, body+" Read ${X_SCIENCE_SKILL_DIR}/references/recipe.txt."),
			name + "/references/recipe.txt": resource,
		})
	}
	if _, err := app.importSkillBundle(name+".zip", "", false, archive("Old instructions.", "OLD_CONFIRMED")); err != nil {
		t.Fatal(err)
	}
	selected, err := app.runtimeSkillsByName([]string{name}, nil)
	if err != nil || len(selected) != 1 {
		t.Fatalf("original selected=%#v err=%v", selected, err)
	}
	prepared, err := app.prepareSkillBundle(name+".zip", "", true, archive("New instructions.", "NEW_CONFIRMED"))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	entered, release, publishStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	readerDone, publisherDone := make(chan error, 1), make(chan error, 1)
	var snapshotFinished atomic.Bool
	go func() {
		readerDone <- app.withPublishedRuntimeSkillSnapshot(selected[0], func() error {
			// Deterministic evidence of the shared lock; no sleep or guessed
			// scheduling delay is used to claim publication/read exclusion.
			if app.skillMutationMu.TryLock() {
				app.skillMutationMu.Unlock()
				close(entered)
				return errors.New("source snapshot did not hold the publication mutex")
			}
			close(entered)
			<-release
			bundle, _, err := loadAgentSkillRuntimeBundle(selected[0])
			if err != nil {
				return err
			}
			if got := publicationTestResource(bundle); got != "OLD_CONFIRMED" {
				return fmt.Errorf("reader mixed replacement bytes into old snapshot: %q", got)
			}
			snapshotFinished.Store(true)
			return nil
		})
	}()
	<-entered
	go func() {
		close(publishStarted)
		_, _, err := app.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
			func([]skillImportResult, []skillImportFailure) error {
				if !snapshotFinished.Load() {
					return errors.New("publisher entered before the old source snapshot finished")
				}
				return nil
			})
		publisherDone <- err
	}()
	<-publishStarted
	close(release)
	if err := <-readerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-publisherDone; err != nil {
		t.Fatal(err)
	}
	// A restored pre-publication closure cannot combine its old instructions
	// with the newly confirmed resource package after waiting for the mutex.
	_, err = app.prepareAgentSkillRuntimeContexts(context.Background(), "unused-stale-session", selected)
	if !errors.Is(err, errSelectedSkillContractUnavailable) {
		t.Fatalf("stale cached contract was accepted or reached workspace I/O: %v", err)
	}
	current, err := app.runtimeSkillsByName([]string{name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle, resources, err := app.snapshotPublishedAgentSkillRuntime(current[0])
	if err != nil || !resources || publicationTestResource(bundle) != "NEW_CONFIRMED" {
		t.Fatalf("confirmed snapshot resources=%t content=%q err=%v", resources, publicationTestResource(bundle), err)
	}
}

func publicationTestResource(bundle agentSkillRuntimeBundle) string {
	for _, file := range bundle.files {
		if file.relativePath == "references/recipe.txt" {
			return string(file.content)
		}
	}
	return ""
}
