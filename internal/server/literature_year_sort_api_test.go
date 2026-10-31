package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureYearSortHTTPAuthorityAndStale(t *testing.T) {
	store, err := workspace.Open(filepath.Join(t.TempDir(), "years.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, row := range []struct{ id, title, metadata string }{{"z", "Alpha", `{"year":"2025"}`}, {"b", "Beta", `{"year":2024}`}, {"a", "Zulu", `{}`}} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner',?,?,'2026-01-01','2026-01-01')`, row.id, row.title, row.metadata); err != nil {
				return err
			}
		}
		return nil
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
	page := func(query url.Values) workspace.LiteraturePage {
		t.Helper()
		response := request("owner", query)
		var value workspace.LiteraturePage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &value) != nil {
			t.Fatalf("actual year reader: HTTP%d %s", response.Code, response.Body.String())
		}
		return value
	}
	cursors := map[string]string{}
	for _, test := range []struct{ mode, first string }{{"year-asc", "b"}, {"year-desc", "z"}} {
		value := page(url.Values{"sort": {test.mode}, "limit": {"1"}})
		if len(value.Items) != 1 || value.Items[0].ID != test.first || value.NextCursor == "" || value.TotalCount == nil || *value.TotalCount != 3 {
			t.Fatalf("year HTTP %s page=%+v", test.mode, value)
		}
		cursors[test.mode] = value.NextCursor
	}
	for _, query := range []url.Values{
		{"sort": {"rating-desc"}},
		{"sort": {"year-asc", "year-desc"}},
		{"sort": {"year-asc"}, "all_item_ids": {"true"}},
		{"sort": {"year-asc"}, "cursor": {cursors["year-desc"]}},
		{"sort": {"title-asc"}, "cursor": {cursors["year-desc"]}},
		{"sort": {"created-desc"}, "cursor": {cursors["year-desc"]}},
		{"cursor": {cursors["year-desc"]}},
		{"sort": {"year-desc"}, "cursor": {cursors["year-desc"]}, "query": {"Alpha"}},
	} {
		if response := request("owner", query); response.Code != http.StatusBadRequest {
			t.Fatalf("year cursor/selection crossed authority: %s HTTP%d", query.Encode(), response.Code)
		}
	}
	if response := request("another-owner", url.Values{"sort": {"year-desc"}, "cursor": {cursors["year-desc"]}}); response.Code != http.StatusBadRequest {
		t.Fatalf("year cursor crossed owner: HTTP%d", response.Code)
	}
	for order, ids := range map[string][]string{"": {"a", "b", "z"}, "title-asc": {"z", "b", "a"}} {
		query := url.Values{"all_item_ids": {"true"}}
		if order != "" {
			query.Set("order", order)
		}
		response := request("owner", query)
		var selection workspace.LiteratureSelection
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &selection) != nil || !reflect.DeepEqual(selection.ItemIDs, ids) {
			t.Fatalf("year sort changed complete membership/export order: HTTP%d %s", response.Code, response.Body.String())
		}
	}
	// Updating an unrelated field preserves the selected year/title anchor.
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json='{"year":2025,"personal_note":"updated"}' WHERE id='z' AND owner_user_id='owner'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	value := page(url.Values{"sort": {"year-desc"}, "cursor": {cursors["year-desc"]}, "limit": {"1"}})
	if len(value.Items) != 1 || value.Items[0].ID != "b" {
		t.Fatal("equivalent manual string/number and unrelated note must preserve continuation")
	}
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json='{"year":2023}' WHERE id='z' AND owner_user_id='owner'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	response := request("owner", url.Values{"sort": {"year-desc"}, "cursor": {cursors["year-desc"]}})
	var stale struct {
		Code string `json:"code"`
	}
	if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &stale) != nil || stale.Code != "LITERATURE_CURSOR_STALE" {
		t.Fatalf("year mutation needs typed first-page recovery: HTTP%d %s", response.Code, response.Body.String())
	}
}
