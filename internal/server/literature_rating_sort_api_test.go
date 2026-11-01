package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureHighestRatedHTTPPageAndCursorAuthority(t *testing.T) {
	store, err := workspace.Open(filepath.Join(t.TempDir(), "rating-sort.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES('z','owner','Alpha','{"rating":5}','2026-01-01','2026-01-01'),('b','owner','Beta','{"rating":4}','2026-01-01','2026-01-01'),('a','owner','Unknown','{"rating":null}','2026-01-01','2026-01-01')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaceStore: store}
	request := func(owner string, query url.Values) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		server.handleLiteratureItemsRead(response, httptest.NewRequest(http.MethodGet, "/api/literature/items?"+query.Encode(), nil), owner)
		return response
	}
	response := request("owner", url.Values{"sort": {"rating-desc"}, "limit": {"1"}})
	var first workspace.LiteraturePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &first) != nil || len(first.Items) != 1 || first.Items[0].ID != "z" || first.NextCursor == "" {
		t.Fatalf("Highest rated must reach the original HTTP reader: HTTP%d %s", response.Code, response.Body.String())
	}
	for _, query := range []url.Values{
		{"sort": {"rating-asc"}},
		{"sort": {"rating-desc", "title-asc"}},
		{"sort": {"rating-desc"}, "all_item_ids": {"true"}},
		{"sort": {"title-asc"}, "cursor": {first.NextCursor}},
		{"sort": {"year-desc"}, "cursor": {first.NextCursor}},
		{"sort": {"rating-desc"}, "cursor": {first.NextCursor}, "query": {"Alpha"}},
	} {
		if response := request("owner", query); response.Code != http.StatusBadRequest {
			t.Fatalf("rating page crossed query authority: %s HTTP%d", query.Encode(), response.Code)
		}
	}
	if response := request("another-owner", url.Values{"sort": {"rating-desc"}, "cursor": {first.NextCursor}}); response.Code != http.StatusBadRequest {
		t.Fatalf("rating cursor crossed owner: HTTP%d", response.Code)
	}
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json='{"rating":3}' WHERE id='z' AND owner_user_id='owner'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	response = request("owner", url.Values{"sort": {"rating-desc"}, "cursor": {first.NextCursor}})
	var stale struct {
		Code string `json:"code"`
	}
	if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &stale) != nil || stale.Code != "LITERATURE_CURSOR_STALE" {
		t.Fatalf("rating anchor mutation needs existing typed recovery: HTTP%d %s", response.Code, response.Body.String())
	}
}
