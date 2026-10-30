package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureSelectionHTTPResolvesAllIDsAndKeepsPagedCounts(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	want := make([]string, 205)
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for index := range want {
			want[index] = fmt.Sprintf("selection-http-%03d", index)
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,reading_status,created_at,updated_at)
 VALUES(?,?,?,'[]','read','2026-01-01','2026-01-01')`, want[index], owner, "Bulk matching reference"); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,reading_status,deleted_at,created_at,updated_at) VALUES
 ('selection-http-foreign','foreign-owner','Bulk matching reference','read',NULL,'2026-01-01','2026-01-01'),
 ('selection-http-deleted',?,'Bulk matching reference','read','2026-01-02','2026-01-01','2026-01-01')`, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=true&query=Bulk&reading_status=read", nil, owner)
	var selection workspace.LiteratureSelection
	var fields map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &selection) != nil ||
		json.Unmarshal(response.Body.Bytes(), &fields) != nil || len(fields) != 3 ||
		selection.Schema != "x-science.literature-selection.v1" || selection.TotalCount != 205 || !reflect.DeepEqual(selection.ItemIDs, want) {
		t.Fatalf("router did not resolve complete IDs without hydrating metadata: HTTP%d %s", response.Code, response.Body.String())
	}
	// Restore legal item metadata only for the ordinary hydrated page comparison.
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json='{}' WHERE owner_user_id=?`, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pageResponse := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?query=Bulk&reading_status=read&limit=100", nil, owner)
	var page workspace.LiteraturePage
	if pageResponse.Code != http.StatusOK || json.Unmarshal(pageResponse.Body.Bytes(), &page) != nil ||
		len(page.Items) != 100 || page.NextCursor == "" || page.TotalCount == nil || *page.TotalCount != selection.TotalCount {
		t.Fatalf("ordinary page did not retain filtered total: HTTP%d %s", pageResponse.Code, pageResponse.Body.String())
	}
	foreign := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=true&query=Bulk", nil, "unrelated-owner")
	if foreign.Code != http.StatusOK || json.Unmarshal(foreign.Body.Bytes(), &selection) != nil || len(selection.ItemIDs) != 0 || selection.TotalCount != 0 {
		t.Fatalf("ID projection leaked another owner's IDs: HTTP%d %s", foreign.Code, foreign.Body.String())
	}
}

func TestLiteratureSelectionHTTPRejectsInvalidAllIDParameters(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	for _, query := range []string{
		"all_item_ids=", "all_item_ids=1", "all_item_ids=TRUE", "all_item_ids=true&all_item_ids=true",
		"all_item_ids=true&limit=1", "all_item_ids=true&limit=", "all_item_ids=true&cursor=",
		"all_item_ids=true&reading_status=", "all_item_ids=true&reading_status=archived",
		"all_item_ids=true&unknown=value", "all_item_ids=true&query=a&query=b",
	} {
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+query, nil, f.f.stream.OwnerID)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid selection parameters %q accepted: HTTP%d %s", query, response.Code, response.Body.String())
		}
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=false&limit=1", nil, f.f.stream.OwnerID)
	var page workspace.LiteraturePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Schema != "x-science.literature-page.v1" || page.TotalCount == nil || *page.TotalCount != 1 {
		t.Fatalf("false flag changed ordinary pagination: HTTP%d %s", response.Code, response.Body.String())
	}
}

func TestLiteratureExportSelectionTitleOrderDoesNotChangeDefault(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	owner := f.f.stream.OwnerID
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for index, title := range []string{"Order beta", "Order Alpha", "Order alpha", "Order Alpha"} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,'{}','2026-01-01','2026-01-01')`, fmt.Sprintf("order-%c", 'a'+index), owner, title); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		suffix string
		ids    []string
	}{{"", []string{"order-a", "order-b", "order-c", "order-d"}}, {"&order=title-asc", []string{"order-b", "order-d", "order-c", "order-a"}}} {
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=true&query=Order"+test.suffix, nil, owner)
		var selection workspace.LiteratureSelection
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &selection) != nil || !reflect.DeepEqual(selection.ItemIDs, test.ids) {
			t.Fatalf("read order %q HTTP%d %s", test.suffix, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/literature/items?order=title-asc", "/api/literature/items?all_item_ids=false&order=title-asc", "/api/literature/items?all_item_ids=true&order=unknown", "/api/literature/items?all_item_ids=true&order=title-asc&cursor=page"} {
		if response := p3JSONRequest(t, f.f.server, http.MethodGet, path, nil, owner); response.Code != 400 {
			t.Fatalf("unsupported order context HTTP%d %s", response.Code, path)
		}
	}
}
