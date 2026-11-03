package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/skills"
	"x-science/internal/testsupport/workspacefixture"
)

func newWebSkillBoundedScanFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	store, err := workspace.Open(workspacefixture.DatabasePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := New(Options{FileRoot: t.TempDir(), Workspace: store})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	const owner = "bounded-scan-owner"
	source := t.TempDir()
	if _, err := server.upsertHostGrant(owner, source, "read"); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(source, "valid", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(valid), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valid, []byte("---\nname: retained-workflow\ndescription: Changed filesystem workflow\n---\nNew uncommitted body."), 0o600); err != nil {
		t.Fatal(err)
	}
	server.skillCatalog.UpsertSkill(skills.Skill{Name: "retained-workflow", Path: valid, Description: "Original workflow", Body: "Original catalog body."})
	return server, owner, source
}

func assertWebSkillScanDidNotPublish(t *testing.T, server *Server, owner string, before []skills.Skill) {
	t.Helper()
	if !reflect.DeepEqual(before, server.skillCatalog.Skills()) {
		t.Fatal("failed/canceled scan published a partial catalog")
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 0 {
		t.Fatalf("failed/canceled scan emitted %d invalidations", len(events))
	}
	history, err := server.loadWebSkillImportHistory(owner)
	if err != nil || len(history) != 0 {
		t.Fatalf("failed/canceled scan wrote import history: err=%v count=%d", err, len(history))
	}
}

func TestWebSkillScanCancellationPreservesCatalog(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Granted source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	for _, route := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/api/skills", nil},
		{http.MethodPost, "/api/skills/import", map[string]any{"skill_path": source}},
	} {
		t.Run(route.path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			request := compatRequest(t, route.method, route.path, owner, route.body).WithContext(ctx)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code >= 200 && recorder.Code < 300 {
				t.Fatalf("canceled source request succeeded: %d", recorder.Code)
			}
			assertWebSkillScanDidNotPublish(t, server, owner, before)
		})
	}
	t.Run("second-source-cancellation", func(t *testing.T) {
		later := t.TempDir()
		if _, err := server.upsertHostGrant(owner, later, "read"); err != nil {
			t.Fatal(err)
		}
		if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{
			{Name: "First source", Path: source}, {Name: "Later source", Path: later},
		}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		firstCompleted := false
		err := server.syncWebExternalSkillsWithLoader(ctx, owner, func(readCtx context.Context, readOwner, path string) ([]skills.Skill, string, error) {
			if sameCleanPath(path, later) {
				if !firstCompleted {
					t.Fatal("the first source did not complete before cancellation")
				}
				cancel()
			}
			items, canonical, err := server.loadGrantedWebSkills(readCtx, readOwner, path)
			if sameCleanPath(path, source) && err == nil {
				firstCompleted = true
			}
			return items, canonical, err
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second source cancellation = %v", err)
		}
		assertWebSkillScanDidNotPublish(t, server, owner, before)
	})
}

func TestWebSkillManifestBoundaryPreservesCatalogAndInputLimit(t *testing.T) {
	if maxSkillArchiveBytes != skills.DefaultMaxManifestBytes || runtimeSkillLoadOptions().MaxManifestBytes != maxSkillArchiveBytes {
		t.Fatal("existing archive/resource input boundary diverged from the manifest policy")
	}
	server, owner, source := newWebSkillBoundedScanFixture(t)
	oversize := filepath.Join(source, "oversize", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(oversize), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(oversize)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("---\nname: oversized-web-metadata\ndescription: Small body\n"); err != nil {
		t.Fatal(err)
	}
	comment := "# " + strings.Repeat("x", 64<<10) + "\n"
	for written := 0; written <= maxSkillArchiveBytes; written += len(comment) {
		if _, err := file.WriteString(comment); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := file.WriteString("---\nSmall body.\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	for _, route := range []struct {
		path string
		body map[string]any
	}{
		{"/api/skills/external-paths", map[string]any{"name": "Granted source", "path": source}},
		{"/api/skills/import", map[string]any{"skill_path": source}},
	} {
		t.Run(route.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodPost, route.path, owner, route.body))
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "skill manifest exceeds") {
				t.Fatalf("oversized complete file status = %d", recorder.Code)
			}
			assertWebSkillScanDidNotPublish(t, server, owner, before)
			paths, err := server.loadWebSkillExternalPaths(owner)
			if err != nil || len(paths) != 0 {
				t.Fatalf("oversized source persisted external paths: err=%v count=%d", err, len(paths))
			}
			if _, err := os.Stat(filepath.Join(server.fileRoot, "skills", "retained-workflow")); !os.IsNotExist(err) {
				t.Fatalf("incomplete source installed a package: %v", err)
			}
		})
	}
	firstSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstSource, "SKILL.md"), []byte("---\nname: first-source-workflow\ndescription: First source\n---\nComplete first source."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := server.upsertHostGrant(owner, firstSource, "read"); err != nil {
		t.Fatal(err)
	}
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{
		{Name: "Complete first source", Path: firstSource}, {Name: "Incomplete later source", Path: source},
	}); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodGet, "/api/skills", owner, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("known catalog became unavailable after incomplete source: %d", recorder.Code)
	}
	retained, found := findCatalogSkill(server.skillCatalog, "retained-workflow")
	if !found || retained.Body != "Original catalog body." {
		t.Fatal("incomplete source changed its retained projection")
	}
	healthy, found := findCatalogSkill(server.skillCatalog, "first-source-workflow")
	if !found || healthy.Body != "Complete first source." {
		t.Fatal("incomplete source blocked an independent complete source")
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 1 {
		t.Fatalf("complete source update signals = %d, want one", len(events))
	}
}

func TestWebSkillOlderScanCannotReplaceNewerProjection(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Granted source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(source, "valid", "SKILL.md")
	writeVersion := func(body string) {
		t.Helper()
		content := "---\nname: retained-workflow\ndescription: Versioned source\n---\n" + body
		if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeVersion("Version one.")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oldRead := make(chan struct{})
	releaseOld := make(chan struct{})
	oldResult := make(chan error, 1)
	go func() {
		oldResult <- server.syncWebExternalSkillsWithLoader(ctx, owner, func(readCtx context.Context, readOwner, path string) ([]skills.Skill, string, error) {
			items, canonical, err := server.loadGrantedWebSkills(readCtx, readOwner, path)
			if err != nil {
				return nil, canonical, err
			}
			close(oldRead)
			select {
			case <-releaseOld:
				return items, canonical, nil
			case <-readCtx.Done():
				return nil, canonical, readCtx.Err()
			}
		})
	}()
	select {
	case <-oldRead:
	case <-ctx.Done():
		t.Fatal("the first scan did not finish reading its source")
	}
	if !server.skillMutationMu.TryLock() {
		t.Fatal("source IO held the publisher mutation guard")
	}
	server.skillMutationMu.Unlock()
	writeVersion("Version two.")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodGet, "/api/skills", owner, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("newer GET status = %d", recorder.Code)
	}
	newer, found := findCatalogSkill(server.skillCatalog, "retained-workflow")
	if !found || newer.Body != "Version two." {
		t.Fatal("the newer scan did not commit version two")
	}
	close(releaseOld)
	select {
	case err := <-oldResult:
		if err != nil {
			t.Fatalf("older scan interrupted the GET path: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the older scan did not return")
	}
	current, found := findCatalogSkill(server.skillCatalog, "retained-workflow")
	if !found || !reflect.DeepEqual(current, newer) {
		t.Fatal("older completed scan replaced the newer catalog projection")
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 1 {
		t.Fatalf("old scan emitted another catalog invalidation: %d", len(events))
	}
}

func TestWebSkillContentCASUsesPublicationBoundary(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	snapshot := server.skillCatalog.Snapshot()
	old, _, err := server.loadGrantedWebSkills(context.Background(), owner, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server.skillMutationMu.Lock()
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		changed, err := server.compareApplyExternalWebSkills(ctx, snapshot, [][]skills.Skill{old})
		if changed {
			err = errors.New("old content crossed the publication boundary")
		}
		finished <- err
	}()
	<-started
	published := skills.Skill{Name: "retained-workflow", Path: filepath.Join(server.fileRoot, "skills", "retained-workflow", "SKILL.md"), Body: "Newly published contract."}
	server.skillCatalog.UpsertSkill(published)
	server.skillMutationMu.Unlock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("content compare/apply did not finish after publication")
	}
	current, found := findCatalogSkill(server.skillCatalog, published.Name)
	if !found || !reflect.DeepEqual(current, published) {
		t.Fatal("old scan overwrote the publication mutation")
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 0 {
		t.Fatal("rejected content emitted a catalog invalidation")
	}
}

func TestWebSkillContentCASPathPolicyAndRegistration(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodPost, "/api/skills/external-paths", owner,
		map[string]any{"name": "Registered source", "path": source}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("registration status = %d", recorder.Code)
	}
	paths, err := server.loadWebSkillExternalPaths(owner)
	if err != nil || len(paths) != 1 || !sameCleanPath(paths[0].Path, source) {
		t.Fatalf("accepted source registration was not saved: %v", err)
	}
	snapshot := server.skillCatalog.Snapshot()
	one := skills.Skill{Name: "first-visible", Path: filepath.Join(source, "one", "SKILL.md"), Body: "First source."}
	two := skills.Skill{Name: "FIRST-VISIBLE", Path: filepath.Join(source, "two", "SKILL.md"), Body: "Conflicting source."}
	changed, err := server.compareApplyExternalWebSkills(context.Background(), snapshot, [][]skills.Skill{{one}, {two}})
	if !changed || err != nil {
		t.Fatalf("complete source plan = %v, %v", changed, err)
	}
	current, found := findCatalogSkill(server.skillCatalog, one.Name)
	if !found || !reflect.DeepEqual(current, one) {
		t.Fatal("later case-insensitive name/path conflict replaced the first source")
	}
	snapshot = server.skillCatalog.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if changed, err := server.compareApplyExternalWebSkills(ctx, snapshot, [][]skills.Skill{{two}}); changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled content commit = %v, %v", changed, err)
	}
	if _, err := server.skillCatalog.CompareApply(context.Background(), snapshot.Revision, nil); err != nil {
		t.Fatalf("canceled content commit advanced revision: %v", err)
	}
	if paths, err = server.loadWebSkillExternalPaths(owner); err != nil || len(paths) != 1 {
		t.Fatalf("content outcome changed the accepted source registration: %v", err)
	}
}

func TestWebSkillOverlappingRootsDoNotInvalidateNetEqualGET(t *testing.T) {
	server, owner, parent := newWebSkillBoundedScanFixture(t)
	nested := filepath.Join(parent, "valid")
	presentation := `{"schema_version":1,"skills":{"retained-workflow":{"category":"parent-presentation","description_i18n":{"en":"Parent-scoped presentation"}}}}`
	if err := os.WriteFile(filepath.Join(parent, "catalog-ui.json"), []byte(presentation), 0o600); err != nil {
		t.Fatal(err)
	}
	parentItems, _, err := server.loadGrantedWebSkills(context.Background(), owner, parent)
	if err != nil {
		t.Fatal(err)
	}
	nestedItems, _, err := server.loadGrantedWebSkills(context.Background(), owner, nested)
	if err != nil {
		t.Fatal(err)
	}
	if len(parentItems) != 1 || len(nestedItems) != 1 || reflect.DeepEqual(parentItems[0], nestedItems[0]) || !sameCleanPath(parentItems[0].Path, nestedItems[0].Path) {
		t.Fatal("legal parent/nested roots did not yield distinct presentation for the same manifest")
	}
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "A parent", Path: parent}, {Name: "B nested", Path: nested}}); err != nil {
		t.Fatal(err)
	}
	server.skillCatalog.UpsertSkill(nestedItems[0])
	snapshot := server.skillCatalog.Snapshot()
	for round := 0; round < 2; round++ {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodGet, "/api/skills", owner, nil))
		if recorder.Code != http.StatusOK || !reflect.DeepEqual(server.skillCatalog.Skills(), snapshot.Skills) {
			t.Fatalf("stable overlapping roots changed the final catalog: status=%d", recorder.Code)
		}
		if events := catalogChangeEvents(t, server, owner, 0); len(events) != 0 {
			t.Errorf("net-equal GET round %d emitted %d feedback invalidations", round, len(events))
		}
		if _, err := server.skillCatalog.CompareApply(context.Background(), snapshot.Revision, nil); err != nil {
			t.Errorf("net-equal GET round %d advanced revision: %v", round, err)
		}
	}
}

func TestWebSkillBusyPublisherDoesNotBlockProjection(t *testing.T) {
	for _, operation := range []string{"GET", "commit", "POST"} {
		t.Run(operation, func(t *testing.T) {
			server, owner, source := newWebSkillBoundedScanFixture(t)
			if operation != "POST" {
				if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Granted source", Path: source}}); err != nil {
					t.Fatal(err)
				}
			}
			items, _, err := server.loadGrantedWebSkills(context.Background(), owner, source)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := server.skillCatalog.Snapshot()
			before := server.skillCatalog.Skills()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type outcome struct {
				status  int
				changed bool
				err     error
			}
			finished := make(chan outcome, 1)
			server.skillMutationMu.Lock()
			locked := true
			defer func() {
				if locked {
					server.skillMutationMu.Unlock()
				}
			}()
			go func() {
				if operation == "commit" {
					changed, err := server.compareApplyExternalWebSkills(ctx, snapshot, [][]skills.Skill{items})
					finished <- outcome{changed: changed, err: err}
					return
				}
				method, path := http.MethodGet, "/api/skills"
				var body map[string]any
				if operation == "POST" {
					method, path = http.MethodPost, "/api/skills/external-paths"
					body = map[string]any{"name": "Registered while publisher busy", "path": source}
				}
				request := compatRequest(t, method, path, owner, body).WithContext(ctx)
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, request)
				finished <- outcome{status: recorder.Code}
			}()
			var result outcome
			returnedWhileHeld := false
			select {
			case result = <-finished:
				returnedWhileHeld = true
			case <-time.After(time.Second): // finite test watchdog, no production delay
			}
			server.skillMutationMu.Unlock()
			locked = false
			if !returnedWhileHeld {
				select {
				case result = <-finished: // always join the blocked r1 operation after releasing its owner
				case <-ctx.Done():
					t.Fatal("projection did not finish after the publication lock was released")
				}
				t.Error("projection waited for the publisher's file/DB work")
			}
			if result.err != nil || result.changed || operation != "commit" && result.status != http.StatusOK {
				t.Errorf("busy projection result = %+v", result)
			}
			if !reflect.DeepEqual(server.skillCatalog.Skills(), before) {
				t.Error("busy projection changed catalog content")
			}
			events := catalogChangeEvents(t, server, owner, 0)
			if operation == "POST" {
				paths, err := server.loadWebSkillExternalPaths(owner)
				if err != nil || len(paths) != 1 || len(events) != 1 || events[0].UserID != owner {
					t.Errorf("accepted registration lost its private setting acknowledgement: paths=%v events=%v err=%v", paths, events, err)
				}
			} else if len(events) != 0 {
				t.Errorf("busy projection emitted %d content invalidations", len(events))
			}
		})
	}
}
