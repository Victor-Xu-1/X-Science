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
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureDateSortHTTPAuthorityAndStale(t *testing.T) {
	store, err := workspace.Open(filepath.Join(t.TempDir(), "dates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for index, row := range []struct{ id, title string }{{"z", "Alpha"}, {"b", "Beta"}, {"a", "Zulu"}} {
			date := time.Date(2026, 1, 1, 0, 0, 0, index+1, time.UTC)
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner',?,'{}',?,?)`, row.id, row.title, date, date); err != nil {
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
			t.Fatalf("actual date HTTP reader: HTTP%d %s", response.Code, response.Body.String())
		}
		return value
	}
	cursors := map[string]string{}
	for _, test := range []struct{ mode, first string }{{"created-asc", "z"}, {"created-desc", "a"}, {"updated-desc", "a"}} {
		value := page(url.Values{"sort": {test.mode}, "limit": {"1"}})
		if len(value.Items) != 1 || value.Items[0].ID != test.first || value.NextCursor == "" {
			t.Fatalf("mode=%s page=%+v", test.mode, value)
		}
		cursors[test.mode] = value.NextCursor
	}
	for _, query := range []url.Values{
		{"sort": {"updated-asc"}},
		{"sort": {"created-asc", "created-desc"}},
		{"sort": {"created-asc"}, "all_item_ids": {"true"}},
		{"sort": {"created-asc"}, "cursor": {cursors["created-desc"]}},
		{"sort": {"title-asc"}, "cursor": {cursors["created-desc"]}},
		{"cursor": {cursors["created-desc"]}},
		{"sort": {"created-desc"}, "cursor": {cursors["created-desc"]}, "query": {"Zulu"}},
	} {
		if response := request("owner", query); response.Code != http.StatusBadRequest {
			t.Fatalf("date cursor/selection crossed authority: query=%s HTTP%d", query.Encode(), response.Code)
		}
	}
	if response := request("another-owner", url.Values{"sort": {"created-desc"}, "cursor": {cursors["created-desc"]}}); response.Code != http.StatusBadRequest {
		t.Fatalf("date cursor crossed owner: HTTP%d", response.Code)
	}
	for order, ids := range map[string][]string{"": {"a", "b", "z"}, "title-asc": {"z", "b", "a"}} {
		query := url.Values{"all_item_ids": {"true"}}
		if order != "" {
			query.Set("order", order)
		}
		response := request("owner", query)
		var selection workspace.LiteratureSelection
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &selection) != nil || !reflect.DeepEqual(selection.ItemIDs, ids) {
			t.Fatalf("complete membership/export order must retain its original authority: HTTP%d %s", response.Code, response.Body.String())
		}
	}
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET updated_at=? WHERE id='a' AND owner_user_id='owner'`, time.Date(2026, 1, 1, 0, 0, 0, 4, time.UTC))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	value := page(url.Values{"sort": {"created-desc"}, "cursor": {cursors["created-desc"]}, "limit": {"1"}})
	if len(value.Items) != 1 || value.Items[0].ID != "b" {
		t.Fatal("updated_at must not invalidate the created_at continuation")
	}
	response := request("owner", url.Values{"sort": {"updated-desc"}, "cursor": {cursors["updated-desc"]}})
	var stale struct {
		Code string `json:"code"`
	}
	if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &stale) != nil || stale.Code != "LITERATURE_CURSOR_STALE" {
		t.Fatalf("date mutation needs existing typed first-page recovery: HTTP%d %s", response.Code, response.Body.String())
	}
}
