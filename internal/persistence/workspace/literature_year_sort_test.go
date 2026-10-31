package workspace

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func yearSortStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "years.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func yearSortPage(t *testing.T, store *Store, mode, cursor string, limit int) LiteraturePage {
	t.Helper()
	page, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursor, limit, LiteraturePageReadOptions{Sort: LiteraturePageSort(mode)})
	if err != nil {
		t.Fatalf("year page %s: %v", mode, err)
	}
	return page
}

func TestLiteratureYearPageOrderingAndExactProjection(t *testing.T) {
	store := yearSortStore(t)
	rows := []struct{ id, title, metadata string }{
		{"z-2025", "Alpha", `{"year":2025}`},
		{"a-2025", "Zulu", `{"year":"2025"}`},
		{"m-2025", "Alpha", `{"year":2025.00000000000000000000}`},
		{"c-2024", "Cedar", `{"year":2.024e3}`},
		{"l-zero", "Lime", `{"year":0}`},
		{"f-zero", "Fig", `{"year":-0.0e99999999999999999999999}`},
		{"p-9999", "Pear", `{"year":"9999"}`},
		{"k-1999", "Kiwi", `{"year":" 01999 "}`},
		{"h-one", "Hazel", `{"year":1.00000e0}`},
		{"unknown-a", "Unknown A", `{}`},
		{"unknown-b", "Unknown B", `{"year":null}`},
		{"unknown-c", "Unknown C", `{"year":"2025-01"}`},
		{"unknown-d", "Unknown D", `{"year":2025.000000000000000000000000000001}`},
		{"unknown-e", "Unknown E", `{"year":-1}`},
		{"unknown-f", "Unknown F", `{"year":1e99999999999999999999999999}`},
		{"unknown-g", "Unknown G", `{"year":1e-99999999999999999999999999}`},
		{"unknown-h", "Unknown H", `{"year":true}`},
		{"unknown-i", "Unknown I", `{"year":[2025]}`},
		{"unknown-j", "Unknown J", `{"year":{"year":2025}}`},
		{"unknown-k", "Unknown K", `{"year":"2e3"}`},
		{"unknown-l", "Unknown L", `{"year":10000}`},
		{"unknown-m", "Unknown M", `{"year":""}`},
		{"unknown-n", "Unknown N", `{"year":0.0001}`},
		{"unknown-o", "Unknown O", `{"Year":2025}`},
	}
	// Exercise a real expanded-note object without converting its opaque number.
	large, err := json.Marshal(map[string]any{"year": json.Number("2025"), "opaque": json.Number("1e400"), "personal_note": strings.Repeat("\x01", 10000), "payload": strings.Repeat("x", 10000)})
	if err != nil {
		t.Fatal(err)
	}
	if err := admitNewLiteratureCatalogMetadata(string(large)); err != nil {
		t.Fatalf("large fixture must be accepted by the original writer: %v", err)
	}
	rows[0].metadata = string(large)
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner',?,?,'2026-01-01','2026-01-01')`, row.id, row.title, row.metadata); err != nil {
				return err
			}
		}
		for _, foreign := range []struct{ id, metadata string }{{"foreign-known", `{"year":9999}`}, {"foreign-unknown", `{}`}} {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'another-owner','Foreign',?,'unknown','unknown')`, foreign.id, foreign.metadata); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,deleted_at,created_at,updated_at) VALUES('deleted','owner','Deleted','{"year":9999}','2026-01-01','unknown','unknown')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unknown := []string{"unknown-a", "unknown-b", "unknown-c", "unknown-d", "unknown-e", "unknown-f", "unknown-g", "unknown-h", "unknown-i", "unknown-j", "unknown-k", "unknown-l", "unknown-m", "unknown-n", "unknown-o"}
	for _, test := range []struct {
		mode  string
		known []string
	}{
		{"year-asc", []string{"f-zero", "l-zero", "h-one", "k-1999", "c-2024", "m-2025", "z-2025", "a-2025", "p-9999"}},
		{"year-desc", []string{"p-9999", "m-2025", "z-2025", "a-2025", "c-2024", "k-1999", "h-one", "f-zero", "l-zero"}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			var ids []string
			cursor := ""
			for attempt := 0; attempt <= len(rows); attempt++ {
				page := yearSortPage(t, store, test.mode, cursor, 2)
				if page.TotalCount == nil || *page.TotalCount != int64(len(rows)) {
					t.Fatalf("year sort changed authorized count: %+v", page.TotalCount)
				}
				for _, item := range page.Items {
					ids = append(ids, item.ID)
					if item.ID == "z-2025" && item.Metadata["opaque"] != json.Number("1e400") {
						t.Fatal("sorting rounded opaque metadata")
					}
				}
				if page.NextCursor == "" {
					break
				}
				if page.NextCursor == cursor || len(page.NextCursor) > 4096 {
					t.Fatal("year cursor did not advance within its existing budget")
				}
				cursor = page.NextCursor
			}
			want := append(append([]string{}, test.known...), unknown...)
			if !reflect.DeepEqual(ids, want) {
				t.Fatalf("year keyset order=%v want=%v", ids, want)
			}
		})
	}
	// The nullable branch remains inside the original collection/project/read scope.
	collection := mustCreateLiteratureCollection(t, store, "owner", "Years", "")
	if _, err := store.CreateProject(CreateProjectInput{ID: "year-project", UserID: "owner", Name: "Years"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"z-2025", "unknown-a"} {
		if _, err := store.SetLiteratureCollectionItem(t.Context(), "owner", collection.CollectionID, id, "collect-"+id, true); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetLiteratureProject(t.Context(), "owner", id, "year-project", "project-"+id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), "owner", LiteratureReadingStatusInput{OperationID: "read-years", ItemIDs: []string{"z-2025", "unknown-a"}, ReadingStatus: LiteratureRead}); err != nil {
		t.Fatal(err)
	}
	filters := LiteratureQueryFilters{ProjectID: "year-project", CollectionID: collection.CollectionID, ReadingStatus: LiteratureRead}
	page, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, "", 1, LiteraturePageReadOptions{Sort: "year-desc"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "z-2025" || page.TotalCount == nil || *page.TotalCount != 2 {
		t.Fatalf("conjunctive year filters: %+v %v", page, err)
	}
	page, err = store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, page.NextCursor, 1, LiteraturePageReadOptions{Sort: "year-desc"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "unknown-a" {
		t.Fatalf("unknown escaped year filters: %+v %v", page, err)
	}
}

func TestLiteratureYearCursorSelectedKeyAndTitleFences(t *testing.T) {
	store := yearSortStore(t)
	create := func(id, title string, metadata map[string]any) LiteratureReceipt {
		t.Helper()
		receipt, err := store.CreateManualLiterature(t.Context(), "owner", LiteratureManualCreate{OperationID: id, Title: title, Metadata: metadata})
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	first := create("year-first", "Alpha", map[string]any{"year": "2025", "opaque": json.Number("1e400")})
	second := create("year-second", "Beta", map[string]any{"year": 2024})
	unknown := create("year-unknown-a", "Unknown A", map[string]any{"year": "bad"})
	create("year-unknown-b", "Unknown B", nil)
	firstPage := yearSortPage(t, store, "year-desc", "", 1)
	if firstPage.Items[0].ID != first.Item.ID || firstPage.NextCursor == "" {
		t.Fatal("manual string year did not determine the anchor")
	}
	patch := LiteraturePatch{OperationID: "year-note", ExpectedRevision: first.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"personal_note":"edited"}}`)}
	patched, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	continued := yearSortPage(t, store, "year-desc", firstPage.NextCursor, 1)
	if continued.Items[0].ID != second.Item.ID {
		t.Fatal("unrelated metadata invalidated year continuation")
	}
	if replay, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, patch); err != nil || !reflect.DeepEqual(replay, patched) {
		t.Fatalf("year reads changed original immutable replay: %+v %v", replay, err)
	}
	patch = LiteraturePatch{OperationID: "year-equivalent", ExpectedRevision: patched.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"year":2.025e3}}`)}
	patched, err = store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	yearSortPage(t, store, "year-desc", firstPage.NextCursor, 1)
	stale := func(cursor string) {
		t.Helper()
		_, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursor, 1, LiteraturePageReadOptions{Sort: "year-desc"})
		if !errors.Is(err, ErrLiteratureCursorStale) {
			t.Fatalf("changed year anchor must be typed stale: %v", err)
		}
	}
	title := "New Alpha"
	patched, err = store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, LiteraturePatch{OperationID: "year-title", ExpectedRevision: patched.Item.MetadataRevision, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	stale(firstPage.NextCursor)
	fresh := yearSortPage(t, store, "year-desc", "", 1)
	patched, err = store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, LiteraturePatch{OperationID: "year-move", ExpectedRevision: patched.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"year":2023}}`)})
	if err != nil {
		t.Fatal(err)
	}
	stale(fresh.NextCursor)
	fresh = yearSortPage(t, store, "year-desc", "", 1)
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), "owner", second.Item.ID, LiteraturePatch{OperationID: "year-delete", ExpectedRevision: second.Item.MetadataRevision, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	stale(fresh.NextCursor)
	for _, mode := range []string{"year-asc", "title-asc", "created-desc", ""} {
		if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, firstPage.NextCursor, 1, LiteraturePageReadOptions{Sort: LiteraturePageSort(mode)}); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("year cursor crossed sort %q: %v", mode, err)
		}
	}
	for _, filters := range []LiteratureQueryFilters{{Query: "Alpha"}, {ProjectID: "other"}, {CollectionID: "other"}, {ReadingStatus: LiteratureRead}, {Lifecycle: LiteratureLifecycleDeleted}} {
		if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, firstPage.NextCursor, 1, LiteraturePageReadOptions{Sort: "year-desc"}); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("year cursor crossed filters %+v: %v", filters, err)
		}
	}
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "another-owner", LiteratureQueryFilters{}, firstPage.NextCursor, 1, LiteraturePageReadOptions{Sort: "year-desc"}); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("year cursor crossed account: %v", err)
	}
	parts := strings.SplitN(firstPage.NextCursor, ".", 2)
	if len(parts) != 2 {
		t.Fatal("year cursor needs a distinct family")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(encoded, &wire) != nil {
		t.Fatal("year cursor is not valid JSON")
	}
	delete(wire, "title_sha256")
	encoded, _ = json.Marshal(wire)
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, parts[0]+"."+base64.RawURLEncoding.EncodeToString(encoded), 1, LiteraturePageReadOptions{Sort: "year-desc"}); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("missing title fence accepted: %v", err)
	}
	filters := LiteratureQueryFilters{Query: "Unknown"}
	unknownPage, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, "", 1, LiteraturePageReadOptions{Sort: "year-desc"})
	if err != nil || len(unknownPage.Items) != 1 || unknownPage.Items[0].ID != unknown.Item.ID {
		t.Fatalf("unknown anchor: %+v %v", unknownPage, err)
	}
	unknown, err = store.PatchLiteratureItem(t.Context(), "owner", unknown.Item.ID, LiteraturePatch{OperationID: "unknown-equivalent", ExpectedRevision: unknown.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"year":"still bad"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, unknownPage.NextCursor, 1, LiteraturePageReadOptions{Sort: "year-desc"}); err != nil {
		t.Fatalf("unknown-to-unknown invalidated effective key: %v", err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), "owner", unknown.Item.ID, LiteraturePatch{OperationID: "unknown-known", ExpectedRevision: unknown.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"year":0}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", filters, unknownPage.NextCursor, 1, LiteraturePageReadOptions{Sort: "year-desc"}); !errors.Is(err, ErrLiteratureCursorStale) {
		t.Fatalf("unknown-to-known anchor not stale: %v", err)
	}
	// Maximum accepted scope/ID strings stay hashed, not expanded into a cursor.
	longOwner, longQuery := strings.Repeat("o", 512), strings.Repeat("x", 512)
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, suffix := range []string{"a", "b"} {
			_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,?,?,'{"year":2025}',?,?)`, strings.Repeat("i", 511)+suffix, longOwner, longQuery, time.Now(), time.Now())
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	longPage, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), longOwner, LiteratureQueryFilters{Query: longQuery}, "", 1, LiteraturePageReadOptions{Sort: "year-asc"})
	if err != nil || longPage.NextCursor == "" || len(longPage.NextCursor) > 4096 {
		t.Fatalf("accepted scope exceeded cursor budget: %+v %v", longPage, err)
	}
}
