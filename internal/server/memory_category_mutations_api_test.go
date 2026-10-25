package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

type memoryCategoryHTTPResult struct {
	workspace.MemoryCategory
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func memoryCategoryReceiptRequest(t *testing.T, app http.Handler, method, target, header, key, body, owner string, wantStatus int) memoryCategoryHTTPResult {
	t.Helper()
	request := newLoopbackTestRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", owner)
	if header != "" {
		request.Header.Set(header, key)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, target, response.Code, wantStatus, response.Body.String())
	}
	var result memoryCategoryHTTPResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid category JSON: %v", err)
	}
	return result
}

func TestMemoryCategoryCompatibilityReplaysDurableKeyAndTypedOutcomes(t *testing.T) {
	database := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{Workspace: store, FileRoot: t.TempDir()})
	app := server.Handler()
	const createBody = `{"name":"Receipt category","guidance":"Original routing guidance","auto_recall":true}`
	const header = "Idempotency-Key"
	first := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "api-create", createBody, "local", http.StatusCreated)
	if first.ID == "" || first.RowCount != 0 || first.Code != "" {
		t.Fatalf("normal201 shape changed: %+v", first)
	}
	closeCtx, closeCancel := context.WithTimeout(t.Context(), 2*time.Second)
	if err := server.Close(closeCtx); err != nil {
		closeCancel()
		_ = store.Close()
		t.Fatal(err)
	}
	closeCancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = workspace.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	server = New(Options{Workspace: store, FileRoot: t.TempDir()})
	app = server.Handler()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
		_ = store.Close()
	})
	// The existing alias reaches the same canonical key after Store/server reopen.
	replay := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", "X-Science-Idempotency-Key", "api-create", createBody, "local", http.StatusCreated)
	if replay.ID != first.ID || !replay.CreatedAt.Equal(first.CreatedAt) || !replay.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("HTTP receipt did not survive reopen")
	}
	path := "/api/memory/categories/" + first.ID
	updateBody := `{"name":"Renamed category"}`
	memoryCategoryReceiptRequest(t, app, http.MethodPut, path, header, "api-update", updateBody, "local", http.StatusOK)
	latest := memoryCategoryReceiptRequest(t, app, http.MethodPut, path, header, "api-later", `{"guidance":"Later routing guidance","auto_recall":false}`, "local", http.StatusOK)
	replay = memoryCategoryReceiptRequest(t, app, http.MethodPut, path, header, "api-update", updateBody, "local", http.StatusOK)
	if replay.Guidance != latest.Guidance || replay.AutoRecall || !replay.UpdatedAt.Equal(latest.UpdatedAt) || replay.Code != "" {
		t.Fatal("old partial HTTP retry overwrote newer fields or changed200 shape")
	}
	conflict := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "api-create", `{"name":"Different request","guidance":"Valid routing guidance"}`, "local", http.StatusConflict)
	if conflict.Code != "MEMORY_CATEGORY_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict code=%q", conflict.Code)
	}
	other := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "api-create", createBody, "other", http.StatusCreated)
	if other.ID == first.ID || other.UserID != "other" {
		t.Fatal("owner/key namespace leaked another user's category")
	}
	rejected := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "invalid-category", `{"name":"profile","guidance":"Valid routing guidance"}`, "local", http.StatusBadRequest)
	if rejected.Code != "MEMORY_CATEGORY_WRITE_REJECTED" {
		t.Fatalf("proven rejection code=%q", rejected.Code)
	}
	rejected = memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "invalid key", createBody, "local", http.StatusBadRequest)
	if rejected.Code != "MEMORY_CATEGORY_WRITE_REJECTED" {
		t.Fatalf("invalid token rejection code=%q", rejected.Code)
	}
	rejected = memoryCategoryReceiptRequest(t, app, http.MethodPut, "/api/memory/categories/absent", header, "absent-category", `{"name":"Not found"}`, "local", http.StatusNotFound)
	if rejected.Code != "MEMORY_CATEGORY_WRITE_REJECTED" {
		t.Fatalf("proven404 code=%q", rejected.Code)
	}
	// A parser400 does not consult the receipt and must not prove the original
	// logical write absent, even if the caller reused an already committed key.
	unstructured := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "api-create", "{", "local", http.StatusBadRequest)
	if unstructured.Code != "" {
		t.Fatalf("arbitrary parser400 acquired certainty: %q", unstructured.Code)
	}
	memoryCompatJSON(t, app, http.MethodDelete, path, nil, "local", http.StatusOK)
	missing := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "api-create", createBody, "local", http.StatusGone)
	if missing.Code != "MEMORY_CATEGORY_COMMITTED_RESULT_MISSING" {
		t.Fatalf("deleted receipt code=%q", missing.Code)
	}
	missing = memoryCategoryReceiptRequest(t, app, http.MethodPut, path, header, "api-update", updateBody, "local", http.StatusGone)
	if missing.Code != "MEMORY_CATEGORY_COMMITTED_RESULT_MISSING" {
		t.Fatalf("deleted update receipt code=%q", missing.Code)
	}
	if rows := memoryCompatArray(t, app, http.MethodGet, "/api/memory/categories", nil, "local", http.StatusOK); len(rows) != 0 {
		t.Fatal("replay recreated a deleted category")
	}
	// Real Store Begin/readback failure stays unknown; generic workspaceStatus
	// would have emitted an uninformative400 for this same storage error.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	unknown := memoryCategoryReceiptRequest(t, app, http.MethodPost, "/api/memory/categories", header, "db-unavailable", createBody, "local", http.StatusServiceUnavailable)
	if unknown.Code != "MEMORY_CATEGORY_OUTCOME_UNKNOWN" || strings.Contains(unknown.Detail, "Original routing guidance") || strings.Contains(unknown.Detail, "database is closed") {
		t.Fatalf("DB failure falsely rejected or leaked private diagnostics: %+v", unknown)
	}
}

func TestMemoryCategoryMutationOutcomePriorityPreservesCertainty(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unknown_no_rows", fmt.Errorf("%w: %w", workspace.ErrMemoryCategoryOutcomeUnknown, sql.ErrNoRows), http.StatusServiceUnavailable, "MEMORY_CATEGORY_OUTCOME_UNKNOWN"},
		{"unknown_wraps_rejection", fmt.Errorf("%w: %w", workspace.ErrMemoryCategoryOutcomeUnknown, fmt.Errorf("%w: %w", workspace.ErrMemoryCategoryWriteRejected, sql.ErrNoRows)), http.StatusServiceUnavailable, "MEMORY_CATEGORY_OUTCOME_UNKNOWN"},
		{"committed_missing_no_rows", fmt.Errorf("%w: %w", workspace.ErrMemoryCategoryCommittedResultMissing, sql.ErrNoRows), http.StatusGone, "MEMORY_CATEGORY_COMMITTED_RESULT_MISSING"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeMemoryCategoryMutationError(response, test.err)
			var body memoryCategoryHTTPResult
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Code != test.code {
				t.Fatalf("nested cause changed authoritative outcome: status=%d code=%q", response.Code, body.Code)
			}
		})
	}
}
