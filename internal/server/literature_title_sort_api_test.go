package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

func copyTitleSortQuery(input url.Values) url.Values {
	output := make(url.Values, len(input))
	for key, values := range input {
		output[key] = append([]string(nil), values...)
	}
	return output
}

func seedTitleSortHTTP(t *testing.T, f *literatureHTTPNumberFixture) {
	t.Helper()
	titles := []struct{ id, title string }{
		{"sort-z", "Sort witness beta"}, {"sort-b", "Sort witness Alpha"},
		{"sort-c", "Sort witness alpha"}, {"sort-d", "Sort witness Alpha"},
		{"sort-e", "Sort witness " + strings.Repeat("z", 4096-len("Sort witness "))},
		{"sort-a", "Sort witness 早"},
	}
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, value := range titles {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,reading_status,created_at,updated_at)
 VALUES(?,?,?,'{}','read','2026-01-01','2026-01-01')`, value.id, f.f.stream.OwnerID, value.title); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,reading_status,created_at,updated_at) VALUES('sort-foreign','foreign-owner','Sort witness A','{}','read','2026-01-01','2026-01-01')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func titleSortHTTPPage(t *testing.T, f *literatureHTTPNumberFixture, query url.Values) workspace.LiteraturePage {
	t.Helper()
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+query.Encode(), nil, f.f.stream.OwnerID)
	var page workspace.LiteraturePage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("title sort page must be accepted: HTTP%d %s", response.Code, response.Body.String())
	}
	return page
}

func TestLiteratureTitleSortHTTPGlobalKeysetAndLegacy(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "")
	seedTitleSortHTTP(t, f)
	for _, test := range []struct {
		sort string
		ids  []string
	}{
		{"title-asc", []string{"sort-b", "sort-d", "sort-c", "sort-z", "sort-e", "sort-a"}},
		{"title-desc", []string{"sort-a", "sort-e", "sort-z", "sort-c", "sort-b", "sort-d"}},
		{"", []string{"sort-a", "sort-b", "sort-c", "sort-d", "sort-e", "sort-z"}},
	} {
		query := url.Values{"query": {"Sort witness"}, "limit": {"2"}}
		if test.sort != "" {
			query.Set("sort", test.sort)
		}
		var ids []string
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber > 3 {
				t.Fatal("cursor failed to advance")
			}
			page := titleSortHTTPPage(t, f, query)
			if page.TotalCount == nil || *page.TotalCount != 6 {
				t.Fatalf("filtered count=%+v", page.TotalCount)
			}
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			if page.NextCursor == "" {
				break
			}
			if len(page.NextCursor) > 4096 {
				t.Fatal("long legal title overflowed the bounded cursor")
			}
			query.Set("cursor", page.NextCursor)
		}
		if !reflect.DeepEqual(ids, test.ids) {
			t.Fatalf("global %q order=%v want=%v", test.sort, ids, test.ids)
		}
	}

	// Preserve all existing scope/ID byte limits even when JSON would escape
	// them sixfold; title cursors must not carry those raw strings.
	longOwner, longQuery := strings.Repeat("<", 512), strings.Repeat("<", 512)
	longID := strings.Repeat("\x01", 512)
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for index, id := range []string{longID, "long-scope-second"} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,'{}','2026-01-01','2026-01-01')`, id, longOwner, longQuery+string(rune('a'+index))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	longScope := url.Values{"query": {longQuery}, "sort": {"title-asc"}, "limit": {"1"}}
	responseLong := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+longScope.Encode(), nil, longOwner)
	var longPage workspace.LiteraturePage
	if responseLong.Code != 200 || json.Unmarshal(responseLong.Body.Bytes(), &longPage) != nil || len(longPage.Items) != 1 || longPage.Items[0].ID != longID || longPage.NextCursor == "" || len(longPage.NextCursor) > 2048 {
		t.Fatalf("legal escaped scope/ID overflowed bounded title cursor: HTTP%d %s", responseLong.Code, responseLong.Body.String())
	}
	longScope.Set("cursor", longPage.NextCursor)
	responseLong = p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+longScope.Encode(), nil, longOwner)
	if responseLong.Code != 200 || json.Unmarshal(responseLong.Body.Bytes(), &longPage) != nil || len(longPage.Items) != 1 || longPage.Items[0].ID != "long-scope-second" {
		t.Fatalf("bounded scope cursor did not resume: HTTP%d %s", responseLong.Code, responseLong.Body.String())
	}

	raw, _ := json.Marshal(struct{ After, Query, Project, Owner string }{After: "sort-c", Query: "Sort witness", Owner: f.f.stream.OwnerID})
	legacy := base64.RawURLEncoding.EncodeToString(raw)
	page := titleSortHTTPPage(t, f, url.Values{"query": {"Sort witness"}, "cursor": {legacy}})
	if len(page.Items) != 3 || page.Items[0].ID != "sort-d" {
		t.Fatalf("original legacy cursor changed: %+v", page.Items)
	}
	response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?all_item_ids=true&query=Sort+witness&order=title-asc", nil, f.f.stream.OwnerID)
	var selection workspace.LiteratureSelection
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &selection) != nil || !reflect.DeepEqual(selection.ItemIDs, []string{"sort-b", "sort-d", "sort-c", "sort-z", "sort-e", "sort-a"}) {
		t.Fatalf("original complete-ID title authority changed: HTTP%d %s", response.Code, response.Body.String())
	}
}

func TestLiteratureTitleSortHTTPBindsScopeAndDetectsStaleAnchors(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "")
	seedTitleSortHTTP(t, f)
	owner := f.f.stream.OwnerID
	collection, err := f.f.store.CreateLiteratureCollection(t.Context(), owner, workspace.LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "Sorted collection"})
	if err != nil {
		t.Fatal(err)
	}
	// Membership order deliberately differs from title order.
	for _, id := range []string{"sort-z", "sort-c", "sort-d", "sort-b"} {
		if _, err := f.f.store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, id, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"query": {"Sort witness"}, "collection_id": {collection.CollectionID}, "reading_status": {"read"}, "sort": {"title-asc"}, "limit": {"1"}}
	first := titleSortHTTPPage(t, f, query)
	if first.Items[0].ID != "sort-b" {
		t.Fatalf("title order did not override membership presentation: %+v", first.Items)
	}
	for _, change := range []struct{ key, value string }{{"sort", "title-desc"}, {"query", "Sort"}, {"reading_status", "unread"}, {"lifecycle", "deleted"}, {"collection_id", "other"}} {
		altered := copyTitleSortQuery(query)
		altered.Set("cursor", first.NextCursor)
		altered.Set(change.key, change.value)
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+altered.Encode(), nil, owner)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("cursor crossed %s: HTTP%d %s", change.key, response.Code, response.Body.String())
		}
	}
	next := copyTitleSortQuery(query)
	next.Set("cursor", first.NextCursor)
	foreign := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+next.Encode(), nil, "foreign-owner")
	if foreign.Code != http.StatusBadRequest {
		t.Fatalf("cursor crossed owner: HTTP%d", foreign.Code)
	}
	// Unrelated metadata revisions do not invalidate a title witness.
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_revision=metadata_revision+1 WHERE id='sort-b' AND owner_user_id=?`, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if page := titleSortHTTPPage(t, f, next); page.Items[0].ID != "sort-d" {
		t.Fatal("metadata-only change disrupted the title cursor")
	}
	assertStale := func(cursor string) {
		t.Helper()
		next.Set("cursor", cursor)
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+next.Encode(), nil, owner)
		var body struct {
			Code string `json:"code"`
		}
		if response.Code != http.StatusConflict || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Code != "LITERATURE_CURSOR_STALE" {
			t.Fatalf("changed anchor needs typed first-page recovery: HTTP%d %s", response.Code, response.Body.String())
		}
	}
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET title='Sort witness Z' WHERE id='sort-b' AND owner_user_id=?`, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertStale(first.NextCursor)
	first = titleSortHTTPPage(t, f, query)
	if _, err := f.f.store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, first.Items[0].ID, uuid.NewString(), false); err != nil {
		t.Fatal(err)
	}
	assertStale(first.NextCursor)
	first = titleSortHTTPPage(t, f, query)
	if err := f.f.store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET deleted_at='2026-01-02' WHERE id=? AND owner_user_id=?`, first.Items[0].ID, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertStale(first.NextCursor)
	// No-sort collection ordering remains the original membership order.
	query.Del("sort")
	if page := titleSortHTTPPage(t, f, query); page.Items[0].ID != "sort-z" {
		t.Fatal("default collection order changed")
	}
}

func TestLiteratureTitleSortHTTPRejectsUnsupportedPresentation(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "")
	for _, query := range []string{
		"sort=", "sort=updated", "sort=TITLE-ASC", "sort=title-asc&sort=title-desc",
		"all_item_ids=true&sort=title-asc", "all_item_ids=true&order=title-asc&sort=title-desc",
		"sort=title-asc&order=title-asc", "sort=title-asc&cursor=old-base64-cursor",
	} {
		response := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items?"+query, nil, f.f.stream.OwnerID)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsupported context %q: HTTP%d", query, response.Code)
		}
	}
}
