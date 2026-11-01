package workspace

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ratingSortStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "rating-sort.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestLiteratureRatingPageExactOrderingAndScope(t *testing.T) {
	store := ratingSortStore(t)
	rows := []struct{ id, title, metadata string }{
		{"top-z", "Alpha", `{"rating":5,"opaque":1e400}`},
		{"top-a", "Zulu", `{"rating":5.0}`},
		{"top-b", "Alpha", `{"rating":5e0}`},
		{"four", "Beta", `{"rating":4}`},
		{"zero-explicit", "Zero", `{"rating":-0.0e999999999999999999}`},
		{"zero-missing", "Zero", `{}`},
		{"unknown-a", "Unknown", `{"rating":null}`},
		{"unknown-b", "Unknown", `{"rating":"5"}`},
		{"unknown-c", "Unknown", `{"rating":5.000000000000000000001}`},
		{"unknown-d", "Unknown", `{"rating":4.999999999999999999999}`},
		{"unknown-e", "Unknown", `{"rating":1e400}`},
		{"unknown-f", "Unknown", `{"rating":6}`},
		{"unknown-g", "Unknown", `{"rating":true}`},
	}
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner',?,?,'2026-01-01','2026-01-01')`, row.id, row.title, row.metadata); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES('foreign','another-owner','First','{"rating":5}','2026-01-01','2026-01-01')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	cursor := ""
	for attempts := 0; attempts <= len(rows); attempts++ {
		page, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursor, 2, LiteraturePageReadOptions{Sort: "rating-desc"})
		if err != nil {
			t.Fatalf("Highest rated must reach the authorized Store query: %v", err)
		}
		if page.TotalCount == nil || *page.TotalCount != int64(len(rows)) {
			t.Fatal("rating order changed authorized membership")
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
			if item.ID == "top-z" && item.Metadata["opaque"] != json.Number("1e400") {
				t.Fatal("rating sort rounded unrelated opaque metadata")
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || len(page.NextCursor) > 4096 {
			t.Fatal("rating cursor failed to advance within its existing bound")
		}
		cursor = page.NextCursor
	}
	want := []string{"top-b", "top-z", "top-a", "four", "zero-explicit", "zero-missing", "unknown-a", "unknown-b", "unknown-c", "unknown-d", "unknown-e", "unknown-f", "unknown-g"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("rating/title/id order=%v want=%v", ids, want)
	}
	collection := mustCreateLiteratureCollection(t, store, "owner", "Rated", "")
	if _, err := store.CreateProject(CreateProjectInput{ID: "rating-project", UserID: "owner", Name: "Rated"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"top-z", "unknown-a"} {
		if _, err := store.SetLiteratureCollectionItem(t.Context(), "owner", collection.CollectionID, id, "collect-"+id, true); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetLiteratureProject(t.Context(), "owner", id, "rating-project", "project-"+id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), "owner", LiteratureReadingStatusInput{OperationID: "read-rated", ItemIDs: []string{"top-z", "unknown-a"}, ReadingStatus: LiteratureRead}); err != nil {
		t.Fatal(err)
	}
	filters := LiteratureQueryFilters{ProjectID: "rating-project", CollectionID: collection.CollectionID, ReadingStatus: LiteratureRead}
	page, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, "", 1, LiteraturePageReadOptions{Sort: "rating-desc"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "top-z" || page.TotalCount == nil || *page.TotalCount != 2 {
		t.Fatalf("conjunctive rating filters: %+v %v", page, err)
	}
	page, err = store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, page.NextCursor, 1, LiteraturePageReadOptions{Sort: "rating-desc"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "unknown-a" {
		t.Fatalf("unknown rating escaped filters: %+v %v", page, err)
	}
}

func TestLiteratureRatingCursorEffectiveKeysAndAuthority(t *testing.T) {
	store := ratingSortStore(t)
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES('a','owner','Alpha','{"rating":5}','2026-01-01','2026-01-01'),('b','owner','Beta','{"rating":4}','2026-01-01','2026-01-01')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, "", 1, LiteraturePageReadOptions{Sort: "rating-desc"})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("rating cursor admission: %+v %v", first, err)
	}
	read := func(owner string, filters LiteratureQueryFilters, mode, cursor string) (LiteraturePage, error) {
		return store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), owner, filters, cursor, 1, LiteraturePageReadOptions{Sort: LiteraturePageSort(mode)})
	}
	for _, mode := range []string{"", "title-asc", "year-desc", "created-desc", "rating-asc"} {
		if _, err := read("owner", LiteratureQueryFilters{}, mode, first.NextCursor); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("rating cursor crossed sort %q: %v", mode, err)
		}
	}
	if _, err := read("another-owner", LiteratureQueryFilters{}, "rating-desc", first.NextCursor); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatal("rating cursor crossed owner")
	}
	if _, err := read("owner", LiteratureQueryFilters{Query: "Alpha"}, "rating-desc", first.NextCursor); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatal("rating cursor crossed query")
	}
	if _, err := read("owner", LiteratureQueryFilters{}, "rating-desc", strings.Repeat("x", 4097)); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatal("rating cursor exceeded existing limit")
	}
	update := func(metadata, title string) {
		t.Helper()
		if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json=?,title=? WHERE id='a' AND owner_user_id='owner'`, metadata, title)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	update(`{"rating":5.0,"personal_note":"unchanged key"}`, "Alpha")
	page, err := read("owner", LiteratureQueryFilters{}, "rating-desc", first.NextCursor)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "b" {
		t.Fatal("equivalent exact rating and unrelated metadata invalidated anchor")
	}
	for _, change := range []struct{ metadata, title string }{
		{`{"rating":4}`, "Alpha"},
		{`{"rating":5}`, "Changed"},
		{`{"rating":null}`, "Alpha"},
	} {
		update(change.metadata, change.title)
		if _, err := read("owner", LiteratureQueryFilters{}, "rating-desc", first.NextCursor); !errors.Is(err, ErrLiteratureCursorStale) {
			t.Fatalf("changed effective rating/title anchor accepted: %v", err)
		}
	}
}
