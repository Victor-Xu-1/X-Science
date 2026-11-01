package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

type annotationReplayRoute struct {
	name, path string
	body       map[string]any
}

func annotationReplayHTTP(t *testing.T, app *Server, method, path, key, owner string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(method, path, bytes.NewReader(raw))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", owner)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}

func annotationReplayRoutes(t *testing.T) (*agentSaveArtifactsFixture, []annotationReplayRoute, workspace.LiteratureReceipt) {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	artifact, version, err := f.store.SaveArtifactVersion(workspace.SaveArtifactVersionInput{ArtifactID: "replay-pdf-source", ProjectID: f.stream.ProjectID, Name: "paper.pdf", Kind: "pdf", Content: pdfReadingContextFixture()})
	if err != nil {
		t.Fatal(err)
	}
	importedResponse := p3JSONRequest(t, f.server, http.MethodPost, "/api/literature/import-artifact-version", workspace.LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: "Synthetic replay source"}, f.stream.OwnerID)
	var imported workspace.LiteratureReceipt
	if importedResponse.Code != http.StatusOK || json.Unmarshal(importedResponse.Body.Bytes(), &imported) != nil || len(imported.Item.Attachments) == 0 || len(imported.Item.Attachments[0].Versions) == 0 {
		t.Fatalf("Library fixture import HTTP%d", importedResponse.Code)
	}
	libraryVersion := imported.Item.Attachments[0].Versions[0]
	return f, []annotationReplayRoute{
		{name: "artifact", path: "/api/artifacts/" + artifact.ID + "/versions/" + version.ID + "/annotations", body: map[string]any{"type": "point", "text": "Original note 🧪", "x_percent": 0, "y_percent": 0, "content_checksum": version.ContentSHA256}},
		{name: "project-target", path: "/api/projects/" + f.stream.ProjectID + "/annotations", body: map[string]any{"target": map[string]any{"kind": "artifact", "artifactId": artifact.ID, "versionId": version.ID}, "content_checksum": version.ContentSHA256, "type": "point", "text": "Original note 🧪", "x_percent": 0, "y_percent": 0}},
		{name: "library", path: "/api/literature/attachment-versions/" + imported.VersionID + "/annotations", body: map[string]any{"type": "text_selection", "text": "Original note 🧪", "selection_text": "Synthetic selection", "page_number": 2, "content_checksum": libraryVersion.Checksum}},
	}, imported
}

func TestAnnotationCreateHTTPReplaysAllThreeAdaptersWithoutOverwritingOrResurrection(t *testing.T) {
	f, routes, _ := annotationReplayRoutes(t)
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			key := "lost-response-" + route.name
			first := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, route.body)
			if first.Code != http.StatusCreated {
				t.Fatalf("create HTTP%d %s", first.Code, first.Body.String())
			}
			original := p3DecodeObject(t, first)
			retry := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, route.body)
			current := p3DecodeObject(t, retry)
			if retry.Code != http.StatusCreated || current["id"] != original["id"] || current["label"] != original["label"] || current["code"] != nil || strings.Contains(retry.Body.String(), key) {
				t.Fatalf("lost-response replay changed canonical DTO or echoed key: HTTP%d", retry.Code)
			}
			foreign := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, "other-owner", route.body)
			if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), original["id"].(string)) {
				t.Fatalf("foreign receipt exposed HTTP%d", foreign.Code)
			}
			changed := map[string]any{}
			for name, value := range route.body {
				changed[name] = value
			}
			changed["text"] = "Different input under original key"
			conflict := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, changed)
			conflictBody := p3DecodeObject(t, conflict)
			if conflict.Code != http.StatusConflict || conflictBody["code"] != "ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT" || conflictBody["detail"] != workspace.ErrMutationIdempotencyConflict.Error() || conflictBody["error"] != conflictBody["detail"] {
				t.Fatalf("changed input lost stable conflict contract: HTTP%d", conflict.Code)
			}
			id := original["id"].(string)
			updated := p3JSONRequest(t, f.server, http.MethodPatch, "/api/annotations/"+id, map[string]any{"text": "Current user edit"}, f.stream.OwnerID)
			if updated.Code != http.StatusOK {
				t.Fatalf("edit HTTP%d", updated.Code)
			}
			retry = annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, route.body)
			current = p3DecodeObject(t, retry)
			if retry.Code != http.StatusCreated || current["id"] != id || current["text"] != "Current user edit" {
				t.Fatal("replay overwrote current edited text")
			}
			deleted := p3JSONRequest(t, f.server, http.MethodDelete, "/api/annotations/"+id, nil, f.stream.OwnerID)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("delete HTTP%d", deleted.Code)
			}
			gone := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, route.body)
			goneBody := p3DecodeObject(t, gone)
			if gone.Code != http.StatusGone || goneBody["code"] != "ANNOTATION_COMMITTED_RESULT_MISSING" || goneBody["error"] != goneBody["detail"] {
				t.Fatalf("deleted note not scoped410: HTTP%d", gone.Code)
			}
			if _, found, err := f.store.GetAnnotation(id); err != nil || found {
				t.Fatal("deleted note resurrected")
			}
		})
	}
}

func TestAnnotationCreateHTTPKeepsLibraryTrashAndArtifactDeletionFences(t *testing.T) {
	f, routes, imported := annotationReplayRoutes(t)
	library := routes[2]
	key := "source-lifetime"
	first := annotationReplayHTTP(t, f.server, http.MethodPost, library.path, key, f.stream.OwnerID, library.body)
	if first.Code != http.StatusCreated {
		t.Fatalf("create HTTP%d", first.Code)
	}
	id := p3DecodeObject(t, first)["id"].(string)
	trash := true
	receipt, err := f.store.PatchLiteratureItem(t.Context(), f.stream.OwnerID, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: imported.Item.MetadataRevision, Deleted: &trash})
	if err != nil {
		t.Fatal(err)
	}
	hidden := annotationReplayHTTP(t, f.server, http.MethodPost, library.path, key, f.stream.OwnerID, library.body)
	if hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), id) || strings.Contains(hidden.Body.String(), "COMMITTED_RESULT") {
		t.Fatalf("Trash disclosed receipt or bypassed source: HTTP%d", hidden.Code)
	}
	trash = false
	if _, err = f.store.PatchLiteratureItem(t.Context(), f.stream.OwnerID, imported.Item.ID, workspace.LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: receipt.Item.MetadataRevision, Deleted: &trash}); err != nil {
		t.Fatal(err)
	}
	restored := annotationReplayHTTP(t, f.server, http.MethodPost, library.path, key, f.stream.OwnerID, library.body)
	if restored.Code != http.StatusCreated || p3DecodeObject(t, restored)["id"] != id {
		t.Fatal("source restore lost the original committed note")
	}
	artifact := routes[0]
	created := annotationReplayHTTP(t, f.server, http.MethodPost, artifact.path, "artifact-lifetime", f.stream.OwnerID, artifact.body)
	if created.Code != http.StatusCreated {
		t.Fatalf("artifact create HTTP%d", created.Code)
	}
	if err := f.store.DeleteArtifact("replay-pdf-source"); err != nil {
		t.Fatal(err)
	}
	unavailable := annotationReplayHTTP(t, f.server, http.MethodPost, artifact.path, "artifact-lifetime", f.stream.OwnerID, artifact.body)
	if unavailable.Code != http.StatusNotFound || strings.Contains(unavailable.Body.String(), "COMMITTED_RESULT") {
		t.Fatalf("deleted source bypassed current fence: HTTP%d", unavailable.Code)
	}
}

func TestAnnotationCreateHTTPNoKeyRemainsIndependentAndExplicitInvalidKeyIsRejected(t *testing.T) {
	f, routes, _ := annotationReplayRoutes(t)
	route := routes[0]
	first := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, "", f.stream.OwnerID, route.body)
	second := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, "", f.stream.OwnerID, route.body)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated || p3DecodeObject(t, first)["id"] == p3DecodeObject(t, second)["id"] {
		t.Fatal("legacy no-key calls were merged or rejected")
	}
	invalid := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, "bad key", f.stream.OwnerID, route.body)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid explicit key was ignored: HTTP%d", invalid.Code)
	}
}

func TestAnnotationCreateHTTPStableCodesPreserveUncertaintyPrecedence(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unknown-over-no-rows-and-source", fmt.Errorf("%w: %w", workspace.ErrAnnotationCreateOutcomeUnknown, errors.Join(sql.ErrNoRows, workspace.ErrAnnotationReadSource, errors.New("private database detail"))), http.StatusServiceUnavailable, "ANNOTATION_CREATE_OUTCOME_UNKNOWN"},
		{"unknown-over-conflict", errors.Join(workspace.ErrAnnotationCreateOutcomeUnknown, workspace.ErrMutationIdempotencyConflict), http.StatusServiceUnavailable, "ANNOTATION_CREATE_OUTCOME_UNKNOWN"},
		{"deleted-original", errors.Join(workspace.ErrAnnotationCommittedResultMissing, sql.ErrNoRows), http.StatusGone, "ANNOTATION_COMMITTED_RESULT_MISSING"},
		{"changed-request", workspace.ErrMutationIdempotencyConflict, http.StatusConflict, "ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT"},
		{"source-unavailable", workspace.ErrAnnotationReadSource, http.StatusNotFound, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeAnnotationCreateError(response, test.err)
			body := p3DecodeObject(t, response)
			if response.Code != test.status || test.code != "" && (body["code"] != test.code || body["error"] != body["detail"]) || test.code == "" && body["code"] != nil {
				t.Fatalf("status/code mismatch: HTTP%d", response.Code)
			}
			if strings.Contains(response.Body.String(), "private database detail") {
				t.Fatal("unknown outcome leaked private failure")
			}
		})
	}
}

func TestAnnotationCreateHTTPChecksExplicitArtifactChecksumBeforeLedger(t *testing.T) {
	f, routes, _ := annotationReplayRoutes(t)
	for _, route := range routes[:2] {
		t.Run(route.name, func(t *testing.T) {
			var baselineNotes int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM annotations`).Scan(&baselineNotes); err != nil {
				t.Fatal(err)
			}
			for index, invalid := range []struct {
				value  any
				status int
			}{
				{strings.Repeat("0", 64), http.StatusConflict}, {nil, http.StatusBadRequest}, {123, http.StatusBadRequest}, {"not-a-hash", http.StatusBadRequest},
			} {
				body := map[string]any{}
				for name, value := range route.body {
					body[name] = value
				}
				body["content_checksum"] = invalid.value
				key := fmt.Sprintf("bad-checksum-%s-%d", route.name, index)
				response := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, body)
				if response.Code != invalid.status {
					t.Fatalf("explicit checksum HTTP%d want%d", response.Code, invalid.status)
				}
				var notes, receipts int
				if err := f.db.QueryRow(`SELECT COUNT(*) FROM annotations`).Scan(&notes); err != nil {
					t.Fatal(err)
				}
				if err := f.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation='annotation.create'`, f.stream.OwnerID, key).Scan(&receipts); err != nil {
					t.Fatal(err)
				}
				if notes != baselineNotes || receipts != 0 {
					t.Fatal("rejected checksum created a note or receipt")
				}
				foreign := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, "other-owner", body)
				if foreign.Code != http.StatusNotFound {
					t.Fatal("checksum processing preceded current owner fence")
				}
			}
			key := "canonical-fill-" + route.name
			first := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, route.body)
			original := p3DecodeObject(t, first)
			if first.Code != http.StatusCreated || original["content_checksum"] != route.body["content_checksum"] {
				t.Fatalf("correct checksum HTTP%d or missing ACK checksum", first.Code)
			}
			omitted := map[string]any{}
			for name, value := range route.body {
				if name != "content_checksum" {
					omitted[name] = value
				}
			}
			expectedNotes := baselineNotes + 1
			for _, empty := range []bool{false, true} {
				body := map[string]any{}
				for name, value := range omitted {
					body[name] = value
				}
				if empty {
					body["content_checksum"] = ""
				}
				if route.name == "artifact" {
					replay := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, body)
					current := p3DecodeObject(t, replay)
					if replay.Code != http.StatusCreated || current["id"] != original["id"] || current["content_checksum"] != route.body["content_checksum"] {
						t.Fatal("direct artifact omitted/empty canonical-fill identity changed")
					}
				} else {
					// The original generic project route stores an omitted/empty
					// checksum as empty. Preserve that legacy request semantics.
					conflict := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, key, f.stream.OwnerID, body)
					if conflict.Code != http.StatusConflict || p3DecodeObject(t, conflict)["code"] != "ANNOTATION_CREATE_IDEMPOTENCY_CONFLICT" {
						t.Fatal("different effective checksum did not conflict")
					}
					legacy := annotationReplayHTTP(t, f.server, http.MethodPost, route.path, "legacy-empty-project-checksum", f.stream.OwnerID, body)
					if legacy.Code != http.StatusCreated || p3DecodeObject(t, legacy)["content_checksum"] != nil {
						t.Fatal("generic project empty-checksum compatibility changed")
					}
					expectedNotes = baselineNotes + 2
				}
			}
			var notes, receipts int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM annotations`).Scan(&notes); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation='annotation.create'`, f.stream.OwnerID, key).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if notes != expectedNotes || receipts != 1 {
				t.Fatal("checksum handling duplicated note/receipt or lost legacy compatibility")
			}
		})
	}
}
