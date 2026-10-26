package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func attachmentInitHTTPRequest(t *testing.T, app http.Handler, body map[string]any, owner string, primary, alias []string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := newLoopbackTestRequest(http.MethodPost, "/api/artifacts/upload/init", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", owner)
	for _, value := range primary {
		request.Header.Add("Idempotency-Key", value)
	}
	for _, value := range alias {
		request.Header.Add("X-Science-Idempotency-Key", value)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func attachmentInitHTTPBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid response JSON: status=%d body=%q err=%v", response.Code, response.Body.String(), err)
	}
	return result
}

func TestAttachmentInitHTTPOptInReplaysAndLegacyRemainsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-init", UserID: "owner-init", Name: "Admission"}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Workspace: store}).Handler()
	const key = "ac0d7d7c-7351-4e53-a5d7-b61d0984e116"
	body := map[string]any{"operation_id": key, "project_id": "project-init", "filename": "source.txt", "content_type": "text/plain", "total_size": 4}
	first := attachmentInitHTTPRequest(t, app, body, "owner-init", []string{key}, []string{key})
	ack := attachmentInitHTTPBody(t, first)
	if first.Code != http.StatusOK || len(ack) != 3 || ack["upload_id"] == "" || ack["chunk_size"] != float64(defaultUploadChunkSize) || ack["total_chunks"] != float64(1) {
		t.Fatalf("opt-in ACK changed response contract: %d %s", first.Code, first.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	app = New(Options{Workspace: store}).Handler()
	replay := attachmentInitHTTPRequest(t, app, body, "owner-init", nil, []string{key})
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("real HTTP/Store reopen did not replay ACK: %d %s", replay.Code, replay.Body.String())
	}
	body["filename"] = "different.txt"
	conflict := attachmentInitHTTPRequest(t, app, body, "owner-init", []string{key}, nil)
	if conflict.Code != http.StatusConflict || attachmentInitHTTPBody(t, conflict)["code"] != "ATTACHMENT_INIT_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed request did not conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	body["filename"] = "source.txt"
	foreign := attachmentInitHTTPRequest(t, app, body, "foreign-owner", []string{key}, nil)
	if foreign.Code != http.StatusNotFound || attachmentInitHTTPBody(t, foreign)["code"] != "ATTACHMENT_INIT_WRITE_REJECTED" || strings.Contains(foreign.Body.String(), ack["upload_id"].(string)) {
		t.Fatalf("foreign owner received original ACK: %d %s", foreign.Code, foreign.Body.String())
	}
	if err := store.CancelAttachmentUpload(t.Context(), "owner-init", ack["upload_id"].(string)); err != nil {
		t.Fatal(err)
	}
	replay = attachmentInitHTTPRequest(t, app, body, "owner-init", []string{key}, nil)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("canceled upload ACK was replaced: %d %s", replay.Code, replay.Body.String())
	}
	delete(body, "operation_id")
	legacyOne := attachmentInitHTTPRequest(t, app, body, "owner-init", []string{key}, nil)
	legacyTwo := attachmentInitHTTPRequest(t, app, body, "owner-init", []string{key}, nil)
	one, two := attachmentInitHTTPBody(t, legacyOne), attachmentInitHTTPBody(t, legacyTwo)
	if legacyOne.Code != http.StatusOK || legacyTwo.Code != http.StatusOK || len(one) != 3 || len(two) != 3 || one["upload_id"] == two["upload_id"] || one["upload_id"] == ack["upload_id"] {
		t.Fatalf("header-only legacy callers no longer receive fresh admission: first=%v second=%v", one, two)
	}
}

func TestAttachmentInitHTTPRejectsInvalidOptInAndKeepsUnknownPriority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "project-init", UserID: "owner-init", Name: "Admission"}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Workspace: store}).Handler()
	const key = "8ddba5cf-7576-4ff8-87be-7be456e9d16a"
	for _, test := range []struct {
		name           string
		operation      any
		primary, alias []string
	}{
		{name: "null", operation: nil, primary: []string{key}},
		{name: "empty", operation: "", primary: []string{key}},
		{name: "invalid", operation: "not-a-uuid", primary: []string{key}},
		{name: "upper-case", operation: strings.ToUpper(key), primary: []string{strings.ToUpper(key)}},
		{name: "wrong-type", operation: 42, primary: []string{key}},
		{name: "missing-header", operation: key},
		{name: "mismatched-header", operation: key, primary: []string{"other"}},
		{name: "mismatched-alias", operation: key, primary: []string{key}, alias: []string{"other"}},
		{name: "explicit-empty-alias", operation: key, primary: []string{key}, alias: []string{""}},
		{name: "repeated-primary", operation: key, primary: []string{key, key}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"operation_id": test.operation, "project_id": "project-init", "filename": "source.txt", "total_size": 4}
			response := attachmentInitHTTPRequest(t, app, body, "owner-init", test.primary, test.alias)
			if response.Code != http.StatusBadRequest || attachmentInitHTTPBody(t, response)["code"] != "ATTACHMENT_INIT_WRITE_REJECTED" {
				t.Fatalf("invalid opt-in reached admission: %d %s", response.Code, response.Body.String())
			}
		})
	}
	strict := attachmentInitHTTPRequest(t, app, map[string]any{"operation_id": key, "project_id": "project-init", "filename": "source.txt", "total_size": 4, "unexpected": true}, "owner-init", []string{key}, nil)
	if strict.Code != http.StatusBadRequest {
		t.Fatalf("strict decoder ignored new unknown input: %d %s", strict.Code, strict.Body.String())
	}
	entries, err := os.ReadDir(path + ".blobs/uploads")
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected request allocated directory: count=%d err=%v", len(entries), err)
	}
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unknown-over-rejected", err: errors.Join(workspace.ErrAttachmentInitOutcomeUnknown, workspace.ErrAttachmentInitWriteRejected, sql.ErrNoRows), status: http.StatusServiceUnavailable, code: "ATTACHMENT_INIT_OUTCOME_UNKNOWN"},
		{name: "unknown-over-conflict", err: errors.Join(workspace.ErrAttachmentInitOutcomeUnknown, workspace.ErrMutationIdempotencyConflict), status: http.StatusServiceUnavailable, code: "ATTACHMENT_INIT_OUTCOME_UNKNOWN"},
		{name: "untyped-no-rows-is-not-proof", err: sql.ErrNoRows, status: http.StatusServiceUnavailable, code: "ATTACHMENT_INIT_OUTCOME_UNKNOWN"},
		{name: "sql-text-is-not-proof", err: errors.New("not found SQL unique secret-diagnostic"), status: http.StatusServiceUnavailable, code: "ATTACHMENT_INIT_OUTCOME_UNKNOWN"},
		{name: "exact-conflict", err: workspace.ErrMutationIdempotencyConflict, status: http.StatusConflict, code: "ATTACHMENT_INIT_IDEMPOTENCY_CONFLICT"},
		{name: "typed-absent-owner", err: errors.Join(workspace.ErrAttachmentInitWriteRejected, sql.ErrNoRows), status: http.StatusNotFound, code: "ATTACHMENT_INIT_WRITE_REJECTED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeAttachmentInitStoreError(response, test.err)
			if response.Code != test.status || attachmentInitHTTPBody(t, response)["code"] != test.code || strings.Contains(response.Body.String(), "secret-diagnostic") {
				t.Fatalf("certainty or sanitized error mapping changed: %d %s", response.Code, response.Body.String())
			}
		})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	unknown := attachmentInitHTTPRequest(t, app, map[string]any{"operation_id": key, "project_id": "project-init", "filename": "source.txt", "total_size": 4}, "owner-init", []string{key}, nil)
	if unknown.Code != http.StatusServiceUnavailable || attachmentInitHTTPBody(t, unknown)["code"] != "ATTACHMENT_INIT_OUTCOME_UNKNOWN" {
		t.Fatalf("closed database was classified as prewrite rejection: %d %s", unknown.Code, unknown.Body.String())
	}
}
