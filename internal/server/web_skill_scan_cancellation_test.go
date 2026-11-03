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
