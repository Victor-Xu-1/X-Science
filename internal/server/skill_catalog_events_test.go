package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/realtime"
)

const catalogChangeEventForTest = "skills.catalog.changed"

func catalogChangeEvents(t *testing.T, server *Server, owner string, after int64) []workspace.RealtimeEvent {
	t.Helper()
	events, err := server.workspaceStore.ListRealtimeEvents(workspace.RealtimeEventFilter{
		UserID: owner, IncludeGlobal: true, Type: catalogChangeEventForTest, AfterSequence: after, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != realtime.DeliveryFanout || len(event.Payload) != 0 || event.ProjectID != "" || event.FrameID != "" || event.RootFrameID != "" {
			t.Fatalf("catalog signal included nonempty/private data: %+v", event)
		}
	}
	return events
}

func catalogChangeCursor(t *testing.T, fixture *kernelSkillDraftFixture) int64 {
	t.Helper()
	events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0)
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Sequence
}

func catalogChangeGuard(t *testing.T, fixture *kernelSkillDraftFixture, mode string, calls *int) (skillDraftCommit, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	guard, err := fixture.server.kernelSkillMutationCommit(ctx, fixture.bound, fixture.identity.access)
	if err != nil {
		t.Fatal(err)
	}
	unknown := kernelruntime.NewHostCallError("outcome_unknown", "synthetic acknowledgement lost after the real guard returned")
	rejected := kernelruntime.NewHostCallError("permission_denied", "synthetic rejection before filesystem mutation")
	injected := unknown
	if mode == "rejected" {
		injected = rejected
	}
	return func(action func() error) error {
		*calls = *calls + 1
		if mode == "rejected" {
			return rejected
		}
		before := catalogChangeCursor(t, fixture)
		if err := guard(action); err != nil {
			return err
		}
		// A signal must not be appended from inside the guarded callback.
		if len(catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, before)) != 0 {
			t.Fatal("catalog event preceded return from the execution guard")
		}
		if mode == "unknown" {
			return unknown
		}
		return nil
	}, injected
}

func TestSkillCatalogPublicationSignalsAfterGuardAndUnknownAcknowledgement(t *testing.T) {
	for _, mode := range []string{"success", "unknown", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "catalog-publication-fixture"
			if _, err := fixture.call("host.skills.edit", name, "SKILL.md", kernelSkillManifestForTest(name, "Published fixture.")); err != nil {
				t.Fatal(err)
			}
			draft, found, err := fixture.server.findCompatibilitySkillDraft(name)
			if err != nil || !found {
				t.Fatalf("draft=%+v found=%t err=%v", draft, found, err)
			}
			snapshot, err := captureSkillDraftSnapshot(draft)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			guard, injected := catalogChangeGuard(t, fixture, mode, &calls)
			installed, err := fixture.server.publishCompatibilitySkillDraftWithCommit(name, false, snapshot.revision, guard)
			if mode == "success" && (err != nil || installed.Name != name) || mode != "success" && !errors.Is(err, injected) {
				t.Fatalf("outcome changed: skill=%+v err=%v", installed, err)
			}
			if mode != "success" && installed.Name != "" {
				t.Fatal("an unknown/rejected outcome acquired a success value")
			}
			_, visible := findCatalogSkill(fixture.server.skillCatalog, name)
			_, fileErr := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md"))
			events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0)
			if mode == "rejected" {
				if visible || !errors.Is(fileErr, os.ErrNotExist) || len(events) != 0 {
					t.Fatalf("rejected publication changed catalog/FS/events: visible=%t file=%v events=%d", visible, fileErr, len(events))
				}
			} else if !visible || fileErr != nil || len(events) != 1 || events[0].UserID != "" {
				t.Fatalf("accepted publication lost projection/signal: visible=%t file=%v events=%+v", visible, fileErr, events)
			}
			if calls != 1 {
				t.Fatalf("publication was retried: %d", calls)
			}
		})
	}
}

func TestSkillCatalogImportBatchCoalescesAndRollbackDoesNotSignal(t *testing.T) {
	for _, mode := range []string{"success", "unknown", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			prepared := []*preparedSkillImport{
				prepareSkillPublicationTest(t, fixture.server, "catalog-batch-a", "First fixture.", false),
				prepareSkillPublicationTest(t, fixture.server, "catalog-batch-b", "Second fixture.", false),
			}
			calls := 0
			guard, injected := catalogChangeGuard(t, fixture, mode, &calls)
			rollback := errors.New("synthetic metadata save rejected before publication visibility")
			_, _, err := fixture.server.publishPreparedSkillImports(prepared, guard, func([]skillImportResult, []skillImportFailure) error {
				if mode == "rollback" {
					return rollback
				}
				return nil
			})
			if mode == "success" && err != nil || mode == "unknown" && !errors.Is(err, injected) || mode == "rollback" && !errors.Is(err, rollback) {
				t.Fatalf("import outcome changed: %v", err)
			}
			events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0)
			want := 1
			if mode == "rollback" {
				want = 0
			}
			if len(events) != want || calls != 1 {
				t.Fatalf("batch signal/retry count=%d/%d want=%d/1", len(events), calls, want)
			}
			for _, item := range prepared {
				_, visible := findCatalogSkill(fixture.server.skillCatalog, item.skill.Name)
				_, fileErr := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", item.skill.Name, "SKILL.md"))
				if mode == "rollback" && (visible || !errors.Is(fileErr, os.ErrNotExist)) || mode != "rollback" && (!visible || fileErr != nil) {
					t.Fatalf("batch catalog/FS diverged: name=%s visible=%t file=%v", item.skill.Name, visible, fileErr)
				}
			}
		})
	}
}

func TestSkillCatalogDeletionUnknownAcknowledgementKeepsVisibleProjection(t *testing.T) {
	for _, mode := range []string{"unknown", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newKernelSkillDraftFixture(t)
			const name = "catalog-delete-fixture"
			if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(kernelSkillManifestForTest(name, "Delete fixture."))); err != nil {
				t.Fatal(err)
			}
			before := catalogChangeCursor(t, fixture)
			calls := 0
			guard, injected := catalogChangeGuard(t, fixture, mode, &calls)
			if err := fixture.server.deleteCompatibilitySkillWithCommit(fixture.stream.OwnerID, name, false, guard); !errors.Is(err, injected) {
				t.Fatalf("delete did not retain original outcome: %v", err)
			}
			_, visible := findCatalogSkill(fixture.server.skillCatalog, name)
			_, fileErr := os.Stat(filepath.Join(fixture.server.fileRoot, "skills", name, "SKILL.md"))
			events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, before)
			if mode == "rejected" {
				if !visible || fileErr != nil || len(events) != 0 {
					t.Fatalf("rejected delete changed data: visible=%t file=%v events=%d", visible, fileErr, len(events))
				}
			} else {
				if visible || !errors.Is(fileErr, os.ErrNotExist) || len(events) != 1 {
					t.Fatalf("unknown delete lost convergence: visible=%t file=%v events=%d", visible, fileErr, len(events))
				}
				if err := fixture.server.deleteCompatibilitySkillWithCommit(fixture.stream.OwnerID, name, false, guard); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing deleted package became an authorized success: %v", err)
				}
				if calls != 1 || len(catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, before)) != 1 {
					t.Fatal("unknown acknowledgement retried deletion or emitted another mutation")
				}
			}
		})
	}
}

func TestSkillCatalogExternalReadbackDoesNotFeedInvalidationLoop(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	root := t.TempDir()
	for _, name := range []string{"external-a", "external-b"} {
		directory := filepath.Join(root, name)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(kernelSkillManifestForTest(name, "External fixture.")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.server.upsertHostGrant(fixture.stream.OwnerID, root, "read_write"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.saveWebSkillExternalPaths(fixture.stream.OwnerID, []webSkillExternalPath{{Name: "fixture", Path: root}}); err != nil {
		t.Fatal(err)
	}
	read := func() {
		request := newLoopbackTestRequest(http.MethodGet, "/api/skills", nil)
		request.Header.Set("X-Science-User-Id", fixture.stream.OwnerID)
		response := httptest.NewRecorder()
		fixture.server.handleWebSkills(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("external read=%d", response.Code)
		}
	}
	read()
	read()
	if events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0); len(events) != 1 || events[0].UserID != "" {
		t.Fatalf("equal GET repeated invalidation or batch was not coalesced: %+v", events)
	}
	if err := os.WriteFile(filepath.Join(root, "external-a", "SKILL.md"), []byte(kernelSkillManifestForTest("external-a", "Changed external fixture.")), 0o600); err != nil {
		t.Fatal(err)
	}
	read()
	read()
	if events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, 0); len(events) != 2 {
		t.Fatalf("real external change or stable follow-up lost: events=%d", len(events))
	}
}

func TestSkillCatalogEnabledNotificationKeepsUserAndLegacyScopes(t *testing.T) {
	fixture := newKernelSkillDraftFixture(t)
	const name = "catalog-enable-fixture"
	if _, err := fixture.server.importSkillBundle(name+".md", "", false, []byte(kernelSkillManifestForTest(name, "Scope fixture."))); err != nil {
		t.Fatal(err)
	}
	skill, _ := findCatalogSkill(fixture.server.skillCatalog, name)
	before := catalogChangeCursor(t, fixture)
	request := newLoopbackTestRequest(http.MethodPut, "/api/skills/catalog/"+name+"/enabled", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("X-Science-User-Id", fixture.stream.OwnerID)
	response := httptest.NewRecorder()
	fixture.server.handleCompatibilityCatalogSkillEnabled(response, request, skill)
	if response.Code != http.StatusOK {
		t.Fatalf("user enable status=%d", response.Code)
	}
	events := catalogChangeEvents(t, fixture.server, fixture.stream.OwnerID, before)
	if len(events) != 1 || events[0].UserID != fixture.stream.OwnerID || len(catalogChangeEvents(t, fixture.server, "other-observer", before)) != 0 {
		t.Fatalf("user preference invalidation escaped its owner: %+v", events)
	}
	before = events[0].Sequence
	request = newLoopbackTestRequest(http.MethodPut, "/api/go/skills/"+name+"/enabled", strings.NewReader(`{"userId":"unselected-body-owner","enabled":false}`))
	response = httptest.NewRecorder()
	fixture.server.handleWorkspaceSkillEnabled(response, request, skill)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy runtime enable status=%d", response.Code)
	}
	events = catalogChangeEvents(t, fixture.server, "other-observer", before)
	if len(events) != 1 || events[0].UserID != "" {
		t.Fatalf("legacy installation change was scoped as private: %+v", events)
	}
	preferences, err := fixture.store.ListSkillPreferences(runtimeSkillPreferenceOwner)
	enabled, configured := preferences[name]
	if err != nil || !configured || enabled {
		t.Fatalf("legacy runtime writer changed: preferences=%v err=%v", preferences, err)
	}
}
