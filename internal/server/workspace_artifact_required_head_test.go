package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

type requiredHeadHTTPFixture struct {
	store    *workspace.Store
	db       *sql.DB
	handler  http.Handler
	artifact workspace.Artifact
	first    workspace.ArtifactVersion
}

func newRequiredHeadHTTPFixture(t *testing.T) requiredHeadHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project", UserID: "owner", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	artifact, first, err := store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{
		ArtifactID: "artifact", ProjectID: "project", Name: "report.txt", Kind: "text/plain", Content: []byte("first"),
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	New(Options{Workspace: store}).registerWorkspaceArtifactReadCompatibilityRoutes(mux)
	return requiredHeadHTTPFixture{store: store, db: db, handler: mux, artifact: artifact, first: first}
}

func (f requiredHeadHTTPFixture) post(t *testing.T, body map[string]any, key, owner string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/api/artifacts/"+f.artifact.ID+"/versions", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", owner)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f requiredHeadHTTPFixture) counts(t *testing.T) map[string]int {
	t.Helper()
	result := make(map[string]int)
	for _, table := range []string{"artifacts", "artifact_versions", "artifact_version_provenance", "workspace_outbox", "mutation_result_ledger"} {
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result[table] = count
	}
	return result
}

func TestArtifactTextVersionRequiresHeadForNewMutation(t *testing.T) {
	for _, key := range []string{"", "stable-legacy-key"} {
		t.Run("key="+key, func(t *testing.T) {
			f := newRequiredHeadHTTPFixture(t)
			before := f.counts(t)
			body := map[string]any{"content": "old client draft", "parent_version_id": f.first.ID,
				"require_expected_head_for_new_mutation": false}
			response := f.post(t, body, key, "owner")
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusPreconditionRequired || payload["code"] != "ARTIFACT_HEAD_REQUIRED" {
				t.Errorf("missing manual head = HTTP%d %s, want typed428", response.Code, response.Body.String())
			}
			if _, exists := payload["details"]; exists {
				t.Error("missing head response disclosed version details")
			}
			if !reflect.DeepEqual(before, f.counts(t)) {
				t.Errorf("missing precondition published data: before=%v after=%v", before, f.counts(t))
			}
			foreign := f.post(t, body, "foreign", "another-owner")
			if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), "ARTIFACT_HEAD_REQUIRED") {
				t.Fatalf("authorization/precondition order = HTTP%d %s", foreign.Code, foreign.Body.String())
			}
		})
	}
}

func TestArtifactTextVersionReplaysCommittedLegacyOperation(t *testing.T) {
	f := newRequiredHeadHTTPFixture(t)
	ctx := workspace.WithMutationIdempotencyKey(context.Background(), "legacy-committed")
	_, legacy, err := f.store.WriteArtifactVersionRealtime(ctx, workspace.WriteArtifactVersionInput{
		ArtifactID: f.artifact.ID, ProjectID: f.artifact.ProjectID, Name: f.artifact.Name,
		ContentType: "text/plain", Content: strings.NewReader("accepted legacy draft"),
		ParentVersionID: f.first.ID, FreshUserEditMappings: true, CarryAnnotationsFromTargetKey: "av:" + f.first.ID,
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	_, newer, err := f.store.WriteArtifactVersion(context.Background(), workspace.WriteArtifactVersionInput{
		ArtifactID: f.artifact.ID, ProjectID: f.artifact.ProjectID, Name: f.artifact.Name,
		ContentType: "text/plain", Content: strings.NewReader("later producer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	before := f.counts(t)
	body := map[string]any{"content": "accepted legacy draft", "parent_version_id": f.first.ID}
	response := f.post(t, body, "legacy-committed", "owner")
	var replay map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || replay["version_id"] != legacy.ID {
		t.Fatalf("legacy receipt replay = HTTP%d %s", response.Code, response.Body.String())
	}
	_, current, _, err := f.store.GetCurrentArtifactVersionMetadata(f.artifact.ID)
	if err != nil || current.ID != newer.ID || !reflect.DeepEqual(before, f.counts(t)) {
		t.Fatal("legacy replay changed current head or mutation counts")
	}
	body["content"] = "changed legacy draft"
	changed := f.post(t, body, "legacy-committed", "owner")
	if changed.Code != http.StatusConflict || !reflect.DeepEqual(before, f.counts(t)) {
		t.Fatalf("changed legacy replay = HTTP%d %s", changed.Code, changed.Body.String())
	}
}

func TestArtifactTextVersionRequiredHeadKeepsConditionalRestore(t *testing.T) {
	f := newRequiredHeadHTTPFixture(t)
	body := map[string]any{"content": "current edit", "parent_version_id": f.first.ID, "expected_head_version_id": f.first.ID}
	first := f.post(t, body, "new-editor", "owner")
	var saved map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusCreated {
		t.Fatalf("new editor = HTTP%d %s", first.Code, first.Body.String())
	}
	before := f.counts(t)
	stale := f.post(t, body, "other-editor", "owner")
	if stale.Code != http.StatusConflict || !reflect.DeepEqual(before, f.counts(t)) {
		t.Fatalf("concurrent editor = HTTP%d %s", stale.Code, stale.Body.String())
	}
	body["expected_head_version_id"] = saved["version_id"]
	body["content"] = "first"
	restore := f.post(t, body, "restore", "owner")
	var restored map[string]any
	if err := json.Unmarshal(restore.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if restore.Code != http.StatusCreated {
		t.Fatalf("historical restore = HTTP%d %s", restore.Code, restore.Body.String())
	}
	_, version, found, err := f.store.GetArtifactVersionMetadata(restored["version_id"].(string))
	if err != nil || !found || version.ParentID != f.first.ID {
		t.Fatalf("historical lineage = %+v %v", version, err)
	}
	before = f.counts(t)
	replay := f.post(t, body, "restore", "owner")
	if replay.Code != http.StatusCreated || !reflect.DeepEqual(before, f.counts(t)) {
		t.Fatalf("restore lost ACK replay = HTTP%d %s", replay.Code, replay.Body.String())
	}
}
