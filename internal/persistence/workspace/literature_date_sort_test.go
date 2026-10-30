package workspace

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func dateSortStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "dates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func dateSortPage(t *testing.T, store *Store, mode, cursor string, limit int) LiteraturePage {
	t.Helper()
	page, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursor, limit, LiteraturePageReadOptions{Sort: LiteraturePageSort(mode)})
	if err != nil {
		t.Fatalf("date page %s: %v", mode, err)
	}
	return page
}

func TestLiteratureDatePageOrderingAndPrecision(t *testing.T) {
	store := dateSortStore(t)
	instant := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []struct {
		id               string
		created, updated any
	}{
		{"z-nano1", instant.Add(time.Nanosecond), instant.Add(4 * time.Nanosecond)},
		{"a-nano2", instant.Add(2 * time.Nanosecond), instant.Add(3 * time.Nanosecond)},
		{"b-equal", "2026-01-01T00:00:00.000000002Z", "2026-01-01T00:00:00.000000003Z"},
		{"n-offset", "2026-01-01T01:00:00.000000002+01:00", "2026-01-01T01:00:00.000000003+01:00"},
		{"m-old", time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC)},
		{"l-future", time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)},
		{"legacy", "2026-01-01 00:00:00", "2026-01-01"},
		{"unknown-a", "not-a-date", "not-a-date"},
		{"unknown-z", "1700000000", "1700000000000"},
	}
	if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner',?,'{}',?,?)`, row.id, row.id, row.created, row.updated); err != nil {
				return err
			}
		}
		// A foreign unknown row must not escape the scope through a keyset OR.
		_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES('foreign','another-owner','Foreign','{}','unknown','unknown')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var rawCreated string
	if err := store.db.QueryRowContext(t.Context(), `SELECT created_at FROM literature_items WHERE id='z-nano1'`).Scan(&rawCreated); err != nil || rawCreated != instant.Add(time.Nanosecond).String() {
		t.Fatalf("fixture must exercise actual time.Time TEXT binding: %q %v", rawCreated, err)
	}
	cursors := map[string]string{}
	for _, test := range []struct {
		mode string
		ids  []string
	}{
		{"created-asc", []string{"m-old", "legacy", "z-nano1", "a-nano2", "b-equal", "n-offset", "l-future", "unknown-a", "unknown-z"}},
		{"created-desc", []string{"l-future", "a-nano2", "b-equal", "n-offset", "z-nano1", "legacy", "m-old", "unknown-a", "unknown-z"}},
		{"updated-desc", []string{"l-future", "z-nano1", "a-nano2", "b-equal", "n-offset", "legacy", "m-old", "unknown-a", "unknown-z"}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			var cursor string
			var ids []string
			for n := 0; n < len(rows); n++ {
				page := dateSortPage(t, store, test.mode, cursor, 2)
				if page.TotalCount == nil || *page.TotalCount != int64(len(rows)) {
					t.Fatalf("unknown dates must stay in the same authorized count: %+v", page.TotalCount)
				}
				for _, item := range page.Items {
					ids = append(ids, item.ID)
				}
				if test.mode == "created-asc" && len(page.Items) > 0 {
					cursors[page.Items[len(page.Items)-1].ID] = page.NextCursor
				}
				if page.NextCursor == "" {
					break
				}
				if cursor == page.NextCursor || len(page.NextCursor) > 4096 {
					t.Fatal("date cursor must advance within the existing budget")
				}
				cursor = page.NextCursor
			}
			if !reflect.DeepEqual(ids, test.ids) {
				t.Fatalf("date keyset order=%v want=%v", ids, test.ids)
			}
		})
	}
	if t.Failed() {
		return
	}
	changeCreated := func(id, value string) {
		t.Helper()
		if _, err := store.db.ExecContext(t.Context(), `UPDATE literature_items SET created_at=? WHERE id=? AND owner_user_id='owner'`, value, id); err != nil {
			t.Fatal(err)
		}
	}
	changeCreated("a-nano2", "2026-01-01T01:00:00.000000002+01:00")
	dateSortPage(t, store, "created-asc", cursors["a-nano2"], 2)
	changeCreated("a-nano2", "2026-01-01T00:00:00.000000003Z")
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursors["a-nano2"], 2, LiteraturePageReadOptions{Sort: "created-asc"}); !errors.Is(err, ErrLiteratureCursorStale) {
		t.Fatalf("one nanosecond anchor change must be stale: %v", err)
	}
	changeCreated("unknown-a", "still-not-a-date")
	dateSortPage(t, store, "created-asc", cursors["unknown-a"], 2)
	changeCreated("unknown-a", "2027-01-01")
	if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursors["unknown-a"], 2, LiteraturePageReadOptions{Sort: "created-asc"}); !errors.Is(err, ErrLiteratureCursorStale) {
		t.Fatalf("unknown-to-known anchor must be stale: %v", err)
	}
	raw, _ := json.Marshal(struct{ After, Query, Project, Owner string }{After: "a-nano2", Owner: "owner"})
	legacy := dateSortPage(t, store, "", base64.RawURLEncoding.EncodeToString(raw), 2)
	if len(legacy.Items) != 2 || legacy.Items[0].ID != "b-equal" {
		t.Fatalf("default legacy cursor was changed: %+v", legacy.Items)
	}
}

func TestLiteratureDateCursorFollowsOnlySelectedDateAndRealReplay(t *testing.T) {
	store := dateSortStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)
	store.now = func() time.Time { return now }
	first, err := store.CreateManualLiterature(t.Context(), "owner", LiteratureManualCreate{OperationID: "create-first", Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Nanosecond)
	second, err := store.CreateManualLiterature(t.Context(), "owner", LiteratureManualCreate{OperationID: "create-second", Title: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	created := dateSortPage(t, store, "created-desc", "", 1)
	updated := dateSortPage(t, store, "updated-desc", "", 1)
	if created.Items[0].ID != second.Item.ID || updated.Items[0].ID != second.Item.ID {
		t.Fatal("actual producer dates must determine the anchor")
	}
	title := "Second edited"
	patch := LiteraturePatch{OperationID: "patch-second", ExpectedRevision: second.Item.MetadataRevision, Title: &title}
	now = now.Add(time.Nanosecond)
	if _, err := store.PatchLiteratureItem(t.Context(), "owner", second.Item.ID, patch); err != nil {
		t.Fatal(err)
	}
	assertCreated := func() {
		t.Helper()
		page := dateSortPage(t, store, "created-desc", created.NextCursor, 1)
		if len(page.Items) != 1 || page.Items[0].ID != first.Item.ID {
			t.Fatal("updating a different date must not invalidate a created cursor")
		}
	}
	assertUpdatedStale := func(cursor string) {
		t.Helper()
		if _, err := store.ListLiteratureItemsInLifecycleWithReadOptions(t.Context(), "owner", LiteratureQueryFilters{}, cursor, 1, LiteraturePageReadOptions{Sort: "updated-desc"}); !errors.Is(err, ErrLiteratureCursorStale) {
			t.Fatalf("updated anchor must detect actual producer mutation: %v", err)
		}
	}
	assertCreated()
	assertUpdatedStale(updated.NextCursor)
	updated = dateSortPage(t, store, "updated-desc", "", 1)
	now = now.Add(time.Hour)
	if _, err := store.PatchLiteratureItem(t.Context(), "owner", second.Item.ID, patch); err != nil {
		t.Fatal(err)
	}
	dateSortPage(t, store, "updated-desc", updated.NextCursor, 1)
	reading := LiteratureReadingStatusInput{OperationID: "mark-second", ItemIDs: []string{second.Item.ID}, ReadingStatus: LiteratureRead}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), "owner", reading); err != nil {
		t.Fatal(err)
	}
	assertCreated()
	assertUpdatedStale(updated.NextCursor)
	updated = dateSortPage(t, store, "updated-desc", "", 1)
	now = now.Add(time.Hour)
	if _, err := store.SetLiteratureReadingStatus(t.Context(), "owner", reading); err != nil {
		t.Fatal(err)
	}
	dateSortPage(t, store, "updated-desc", updated.NextCursor, 1)
}
