package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sessionstore "x-science/internal/persistence/sessions"
	"x-science/internal/skills"
)

func TestSelectedSkillPreparationRejectsPendingInlinePublication(t *testing.T) {
	testSelectedSkillPendingPublication(t, false)
}

func TestSelectedSkillPreparationRejectsPendingReplacementResources(t *testing.T) {
	testSelectedSkillPendingPublication(t, true)
}

func testSelectedSkillPendingPublication(t *testing.T, resources bool) {
	t.Helper()
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "runtime.db"), true)
	defer closeKernelHostTestRuntime(t, app, manager, store)
	const sessionID = "selected-publication-session"
	if err := app.sessionStore.Upsert(sessionstore.Session{ID: sessionID, WorkDir: identity.workspaceDir,
		Orchestration: map[string]any{"frame_id": "root-routine"}}); err != nil {
		t.Fatal(err)
	}
	const name = "selected-publication-fixture"
	body := "Old authorized inline instructions."
	if resources {
		body += " Read ${X_SCIENCE_SKILL_DIR}/references/recipe.txt before answering."
	}
	archive := func(content string) []byte {
		files := map[string]string{name + "/SKILL.md": kernelSkillManifestForTest(name, body)}
		if resources {
			files[name+"/references/recipe.txt"] = content
		}
		return buildSkillArchive(t, files)
	}
	if _, err := app.importSkillBundle(name+".zip", "", false, archive("OLD_CONFIRMED_RESOURCE")); err != nil {
		t.Fatal(err)
	}
	cached, err := app.runtimeSkillsByName([]string{name}, nil)
	if err != nil || len(cached) != 1 {
		t.Fatalf("original selected catalog=%#v err=%v", cached, err)
	}
	prepared, err := app.prepareSkillBundle(name+".zip", "", true, archive("NEW_UNCONFIRMED_RESOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	observedPending := false
	_, _, err = app.publishPreparedSkillImports([]*preparedSkillImport{prepared}, immediateSkillDraftCommit,
		func([]skillImportResult, []skillImportFailure) error {
			observedPending = true
			if !unpublishedManagedSkillPath(cached[0].Path, app.fileRoot) {
				t.Fatal("real replacement publication did not expose its pending marker")
			}
			// Match the ordinary runner's explicit-selection/dependency preparation.
			selected, selectionErr := app.runtimeSkillsByName([]string{name}, nil)
			if selectionErr == nil {
				selected, selectionErr = app.expandRuntimeSkillDependencies(selected, nil, nil, false, nil)
			}
			if selectionErr == nil {
				selected, selectionErr = app.prepareAgentSkillRuntimeContexts(context.Background(), sessionID, selected)
			}
			assertPendingSelectedSkillRejected(t, selected, selectionErr)
			// Recovery can already hold the cached closure before publication starts.
			recovered, recoveryErr := app.prepareAgentSkillRuntimeContexts(context.Background(), sessionID, cached)
			assertPendingSelectedSkillRejected(t, recovered, recoveryErr)
			return nil
		})
	if err != nil || !observedPending {
		t.Fatalf("publication callback=%t err=%v", observedPending, err)
	}
	selected, err := app.runtimeSkillsByName([]string{name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = app.prepareAgentSkillRuntimeContexts(context.Background(), sessionID, selected)
	if err != nil || len(selected) != 1 {
		t.Fatalf("confirmed selected preparation=%#v err=%v", selected, err)
	}
	if resources {
		content, err := os.ReadFile(filepath.Join(filepath.Dir(selected[0].Path), "references", "recipe.txt"))
		if err != nil || string(content) != "NEW_UNCONFIRMED_RESOURCE" {
			t.Fatalf("confirmed resource=%q err=%v", content, err)
		}
	}
}

func assertPendingSelectedSkillRejected(t *testing.T, selected []skills.Skill, err error) {
	t.Helper()
	if !errors.Is(err, errSelectedSkillContractUnavailable) {
		leaked := ""
		if len(selected) > 0 && strings.Contains(selected[0].Body, "recipe.txt") {
			content, _ := os.ReadFile(filepath.Join(filepath.Dir(selected[0].Path), "references", "recipe.txt"))
			leaked = string(content)
		}
		t.Errorf("pending selected/recovered Skill reached preparation: err=%v selected=%d materialized_resource=%q", err, len(selected), leaked)
	}
}
