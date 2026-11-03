package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	settingstore "x-science/internal/persistence/settings"
	"x-science/internal/skills"
)

func TestWebSkillRemovedSourceRejectsLateScanWithoutContentMutation(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	server.skillCatalog.RemoveSkillsUnder(source)
	before := server.skillCatalog.Snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readComplete := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- server.syncWebExternalSkillsWithLoader(ctx, owner, func(readCtx context.Context, readOwner, path string) ([]skills.Skill, string, error) {
			items, canonical, err := server.loadGrantedWebSkills(readCtx, readOwner, path)
			if err != nil {
				return nil, canonical, err
			}
			close(readComplete)
			select {
			case <-release:
				return items, canonical, nil
			case <-readCtx.Done():
				return nil, canonical, readCtx.Err()
			}
		})
	}()
	select {
	case <-readComplete:
	case <-ctx.Done():
		t.Fatal("the scan did not finish reading its registered source")
	}
	deleted := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleted, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("source removal status = %d", deleted.Code)
	}
	paths, err := server.loadWebSkillExternalPaths(owner)
	if err != nil || len(paths) != 0 {
		t.Fatalf("source was not durably removed: %v, %v", paths, err)
	}
	if !reflect.DeepEqual(before.Skills, server.skillCatalog.Skills()) {
		t.Fatal("zero-entry source removal unexpectedly changed content")
	}
	if _, err := server.skillCatalog.CompareApply(context.Background(), before.Revision, nil); err != nil {
		t.Fatalf("zero-entry source removal changed the content revision: %v", err)
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("late GET interrupted normal catalog readback: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the late scan did not return")
	}
	if !reflect.DeepEqual(before.Skills, server.skillCatalog.Skills()) {
		t.Fatal("late scan republished content from the removed source")
	}
	for _, event := range catalogChangeEvents(t, server, owner, 0) {
		if event.UserID == "" {
			t.Fatal("late scan emitted a global content invalidation after source removal")
		}
	}
}

func TestWebSkillGrantRevokeRejectsCapturedSource(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	authority, err := server.captureWebSkillSourceSnapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.close()
	items, _, err := loadWebSkillsWithGrants(context.Background(), source, authority.grants)
	if err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodDelete, "/api/preferences/host-grants", owner, map[string]any{"path": source}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("grant revoke status = %d", recorder.Code)
	}
	changed, err := server.compareApplyExternalWebSkills(context.Background(), authority.catalog, [][]skills.Skill{items}, authority)
	if changed || err != nil || !reflect.DeepEqual(before, server.skillCatalog.Skills()) {
		t.Fatalf("revoked grant admitted old content: changed=%v err=%v", changed, err)
	}
	if _, err := server.updateWebSkillSourceSnapshot(context.Background(), authority, func(paths []webSkillExternalPath) ([]webSkillExternalPath, error) { return paths, nil }); !errors.Is(err, settingstore.ErrSnapshotConflict) {
		t.Fatalf("old POST authority survived grant revocation: %v", err)
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 0 {
		t.Fatal("rejected source emitted content invalidation")
	}
}

func TestWebSkillSourceUpdateCannotResurrectRemovedRegistration(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	oldPost, err := server.captureWebSkillSourceSnapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	defer oldPost.close()
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("source DELETE status = %d", recorder.Code)
	}
	if _, err := server.updateWebSkillSourceSnapshot(context.Background(), oldPost, func(paths []webSkillExternalPath) ([]webSkillExternalPath, error) {
		return append(paths, webSkillExternalPath{Name: "Old POST", Path: source}), nil
	}); !errors.Is(err, settingstore.ErrSnapshotConflict) {
		t.Fatalf("late POST did not report source conflict: %v", err)
	}
	paths, err := server.loadWebSkillExternalPaths(owner)
	if err != nil || len(paths) != 0 {
		t.Fatalf("old POST resurrected a removed registration: %v %v", paths, err)
	}
	for _, key := range []string{webSkillExternalPathsSetting, webSkillUserSettingKey(webSkillExternalPathsSetting, owner)} {
		if _, err := server.executeSettingsTool("settings_set", map[string]any{"key": key, "value": []any{}}); err == nil || !strings.Contains(err.Error(), "Skill source API") {
			t.Fatalf("generic settings writer bypassed source authority: %v", err)
		}
	}
}

func TestWebSkillSourceGrantOwnerBusySkipsContent(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	authority, err := server.captureWebSkillSourceSnapshot(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	defer authority.close()
	items, _, err := loadWebSkillsWithGrants(context.Background(), source, authority.grants)
	if err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	server.hostGrantKernelMu.Lock()
	go func() {
		changed, err := server.compareApplyExternalWebSkills(ctx, authority.catalog, [][]skills.Skill{items}, authority)
		if changed {
			err = errors.New("busy grant owner admitted content")
		}
		finished <- err
	}()
	returnedWhileHeld := false
	select {
	case err = <-finished:
		returnedWhileHeld = true
	case <-time.After(time.Second):
	}
	server.hostGrantKernelMu.Unlock()
	if !returnedWhileHeld {
		select {
		case err = <-finished:
		case <-ctx.Done():
			t.Fatal("content operation did not finish after releasing its grant owner")
		}
		t.Error("content operation waited on its busy grant owner")
	}
	if err != nil || !reflect.DeepEqual(before, server.skillCatalog.Skills()) || len(catalogChangeEvents(t, server, owner, 0)) != 0 {
		t.Fatalf("busy grant owner changed content or notifications: %v", err)
	}
}

func TestWebSkillSourceDeleteBusyPublisherRejectsWithoutSideEffects(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan int, 1)
	server.skillMutationMu.Lock()
	go func() {
		request := compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil).WithContext(ctx)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		finished <- recorder.Code
	}()
	returnedWhileHeld := false
	status := 0
	select {
	case status = <-finished:
		returnedWhileHeld = true
	case <-time.After(time.Second):
	}
	server.skillMutationMu.Unlock()
	if !returnedWhileHeld {
		select {
		case status = <-finished:
		case <-ctx.Done():
			t.Fatal("DELETE did not return after its publication owner was released")
		}
		t.Error("DELETE cleanup waited on the publication owner")
	}
	if status != http.StatusConflict {
		t.Errorf("busy source deletion status = %d", status)
	}
	paths, err := server.loadWebSkillExternalPaths(owner)
	if err != nil || len(paths) != 1 {
		t.Fatalf("busy DELETE changed its prior settings: %v %v", paths, err)
	}
	if !reflect.DeepEqual(before, server.skillCatalog.Skills()) {
		t.Error("busy DELETE cleanup removed the publisher's current projection")
	}
	events := catalogChangeEvents(t, server, owner, 0)
	if len(events) != 0 {
		t.Errorf("busy rejected DELETE emitted notifications: %v", events)
	}
}

func TestWebSkillSourceDeleteCompletesBeforeLaterRegistration(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Initial source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	deleted := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleted, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("source DELETE status = %d", deleted.Code)
	}
	if _, found := findCatalogSkill(server.skillCatalog, "retained-workflow"); found {
		t.Fatal("successful DELETE retained a projection without any source to rescan")
	}
	registered := httptest.NewRecorder()
	server.Handler().ServeHTTP(registered, compatRequest(t, http.MethodPost, "/api/skills/external-paths", owner,
		map[string]any{"name": "Later registration", "path": source}))
	if registered.Code != http.StatusOK {
		t.Fatalf("later POST status = %d", registered.Code)
	}
	later, found := findCatalogSkill(server.skillCatalog, "retained-workflow")
	if !found || later.Body != "New uncommitted body." {
		t.Fatal("later registration did not publish current content")
	}
	readback := httptest.NewRecorder()
	server.Handler().ServeHTTP(readback, compatRequest(t, http.MethodGet, "/api/skills", owner, nil))
	current, found := findCatalogSkill(server.skillCatalog, later.Name)
	if readback.Code != http.StatusOK || !found || !reflect.DeepEqual(current, later) {
		t.Fatal("completed DELETE had late cleanup that removed the later registration")
	}
	if events := catalogChangeEvents(t, server, owner, 0); len(events) != 2 {
		t.Fatalf("accepted DELETE/POST produced unexpected invalidations: %d", len(events))
	}
	t.Run("unregistered-source", func(t *testing.T) {
		other, actor, path := newWebSkillBoundedScanFixture(t)
		before := other.skillCatalog.Skills()
		response := httptest.NewRecorder()
		other.Handler().ServeHTTP(response, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(path), actor, nil))
		if response.Code != http.StatusOK || !reflect.DeepEqual(before, other.skillCatalog.Skills()) || len(catalogChangeEvents(t, other, actor, 0)) != 0 {
			t.Fatal("an unregistered path authorized clearing a projection")
		}
	})
}

func TestWebSkillSourceDeleteSaveFailurePreservesProjection(t *testing.T) {
	server, owner, source := newWebSkillBoundedScanFixture(t)
	if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
		t.Fatal(err)
	}
	before := server.skillCatalog.Skills()
	blockedSave := filepath.Join(server.fileRoot, "settings.json.tmp")
	if err := os.Mkdir(blockedSave, 0o700); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil))
	if recorder.Code >= 200 && recorder.Code < 300 {
		t.Fatal("failed settings save was reported as an accepted deletion")
	}
	paths, err := server.loadWebSkillExternalPaths(owner)
	if err != nil || len(paths) != 1 || !reflect.DeepEqual(before, server.skillCatalog.Skills()) || len(catalogChangeEvents(t, server, owner, 0)) != 0 {
		t.Fatalf("failed save changed sources/content/notifications: %v", err)
	}
	if !server.skillMutationMu.TryLock() {
		t.Fatal("failed source mutation retained the publisher guard")
	}
	server.skillMutationMu.Unlock()
	if err := os.Remove(blockedSave); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("a later explicit retry could not finish deletion: %d", recorder.Code)
	}
}

// This test Context cancels if Err is consulted after the real settings file
// records removal. It does not count Err calls, sleep, or hook production IO.
type sourceDeleteAfterSaveContext struct {
	context.Context
	cancel context.CancelFunc
	path   string
	key    string
}

func (c *sourceDeleteAfterSaveContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil
	}
	var settings struct {
		Settings map[string]struct{ Value json.RawMessage } `json:"settings"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	if setting, found := settings.Settings[c.key]; found {
		var paths []webSkillExternalPath
		if json.Unmarshal(setting.Value, &paths) == nil && len(paths) == 0 {
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestWebSkillSourceDeleteCancellationRespectsAcceptedSave(t *testing.T) {
	for _, stage := range []string{"before-save", "after-save-witness"} {
		t.Run(stage, func(t *testing.T) {
			server, owner, source := newWebSkillBoundedScanFixture(t)
			if err := server.saveWebSkillExternalPaths(owner, []webSkillExternalPath{{Name: "Registered source", Path: source}}); err != nil {
				t.Fatal(err)
			}
			before := server.skillCatalog.Skills()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ctx context.Context = base
			if stage == "before-save" {
				cancel()
			} else {
				ctx = &sourceDeleteAfterSaveContext{Context: base, cancel: cancel, path: filepath.Join(server.fileRoot, "settings.json"), key: webSkillUserSettingKey(webSkillExternalPathsSetting, owner)}
			}
			recorder := httptest.NewRecorder()
			request := compatRequest(t, http.MethodDelete, "/api/skills/external-paths?path="+url.QueryEscape(source), owner, nil).WithContext(ctx)
			server.Handler().ServeHTTP(recorder, request)
			paths, err := server.loadWebSkillExternalPaths(owner)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before-save" {
				if recorder.Code >= 200 && recorder.Code < 300 || len(paths) != 1 || !reflect.DeepEqual(before, server.skillCatalog.Skills()) || len(catalogChangeEvents(t, server, owner, 0)) != 0 {
					t.Fatal("cancellation before acceptance changed settings/content")
				}
			} else {
				if recorder.Code != http.StatusOK || len(paths) != 0 {
					t.Fatal("post-save cancellation witness lost the accepted setting deletion")
				}
				if _, found := findCatalogSkill(server.skillCatalog, "retained-workflow"); found || !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("post-save cancellation check prevented required cleanup")
				}
			}
			if !server.skillMutationMu.TryLock() {
				t.Fatal("source mutation retained its publisher guard")
			}
			server.skillMutationMu.Unlock()
		})
	}
}
