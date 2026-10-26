package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

type attachmentJSONBoundaryBody struct {
	reader io.Reader
	bytes  int
	closed bool
}

func (b *attachmentJSONBoundaryBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.bytes += n
	return n, err
}

func (b *attachmentJSONBoundaryBody) Close() error { b.closed = true; return nil }

type attachmentJSONBoundaryReadError struct{ err error }

func (r attachmentJSONBoundaryReadError) Read([]byte) (int, error) { return 0, r.err }

func TestAttachmentJSONBodyBoundaryUsesActualEOF(t *testing.T) {
	const limit = 1 << 20
	const prefix = `{"filename":"fixture.txt"}`
	exact := prefix + strings.Repeat(" ", limit-len(prefix))
	for _, test := range []struct {
		name, body    string
		contentLength int64
		wantError     bool
		readError     error
	}{
		{name: "valid_small", body: prefix},
		{name: "valid_exact_limit", body: exact, contentLength: limit},
		{name: "oversized_padding_unknown_length", body: exact + " ", contentLength: -1, wantError: true},
		{name: "hidden_suffix_after_limit_with_short_header", body: exact + `{}`, contentLength: 1, wantError: true},
		{name: "oversized_padding_reads_at_most_one_extra_byte", body: exact + strings.Repeat(" ", limit), wantError: true},
		{name: "large_declared_length_does_not_reject_valid_body", body: prefix, contentLength: limit + 1},
		{name: "ordinary_second_value", body: prefix + `{}`, wantError: true},
		{name: "unknown_field", body: `{"unexpected":true}`, wantError: true},
		{name: "truncated_json", body: `{"filename":`, wantError: true},
		{name: "oversized_single_value", body: `{"filename":"` + strings.Repeat("x", limit) + `"}`, wantError: true},
		{name: "read_error_after_value", body: prefix, wantError: true, readError: io.ErrUnexpectedEOF},
		{name: "context_error_after_exact_limit", body: exact, wantError: true, readError: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(test.body)
			if test.readError != nil {
				reader = io.MultiReader(reader, attachmentJSONBoundaryReadError{test.readError})
			}
			body := &attachmentJSONBoundaryBody{reader: reader}
			request := httptest.NewRequest(http.MethodPost, "/api/artifacts/upload/init", nil)
			request.Body, request.ContentLength = body, test.contentLength
			var target struct {
				Filename string `json:"filename"`
			}
			err := decodeAttachmentJSON(request, &target)
			if (err != nil) != test.wantError {
				t.Errorf("decode error=%v, want rejection=%t", err, test.wantError)
			}
			if test.readError != nil && !errors.Is(err, test.readError) {
				t.Errorf("read failure was hidden: got=%v want=%v", err, test.readError)
			}
			if !test.wantError && target.Filename != "fixture.txt" {
				t.Errorf("valid value changed: %+v", target)
			}
			if body.bytes > limit+1 || body.closed || request.Body != body {
				t.Errorf("decoder exceeded its read budget or took ownership: bytes=%d closed=%t body_replaced=%t", body.bytes, body.closed, request.Body != body)
			}
		})
	}
}

func TestAttachmentUploadInitOversizedJSONHasNoSideEffects(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := workspace.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(workspace.CreateProjectInput{ID: "json-boundary-project", UserID: "json-boundary-user", Name: "Boundary"}); err != nil {
		t.Fatal(err)
	}
	readDB, err := sql.Open("sqlite", "file:"+databasePath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readDB.Close() })
	countRows := func() int {
		t.Helper()
		var count int
		if err := readDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM attachment_uploads").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countRows(); count != 0 {
		t.Fatalf("upload fixture is not empty: %d", count)
	}
	const prefix = `{"project_id":"json-boundary-project","filename":"fixture.txt","total_size":1}`
	body := prefix + strings.Repeat(" ", (1<<20)+1-len(prefix))
	request := newLoopbackTestRequest(http.MethodPost, "/api/artifacts/upload/init", strings.NewReader(body))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Science-User-Id", "json-boundary-user")
	response := httptest.NewRecorder()
	New(Options{Workspace: store}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Errorf("oversized init status=%d, want400; body=%s", response.Code, response.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := json.Unmarshal(envelope["detail"], &detail); err != nil || detail != "request body exceeds 1 MiB" {
		t.Errorf("size rejection did not use the existing detail envelope: detail=%q err=%v", detail, err)
	}
	if _, acknowledged := envelope["upload_id"]; acknowledged {
		t.Errorf("oversized body received an upload ACK: %s", response.Body.String())
	}
	if count := countRows(); count != 0 {
		t.Errorf("oversized init created %d upload rows", count)
	}
	entries, err := os.ReadDir(filepath.Join(databasePath+".blobs", "uploads"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("oversized init created %d upload directories", len(entries))
	}
}
