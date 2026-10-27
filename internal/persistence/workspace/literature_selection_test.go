package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureSelectionReturnsAllIDsBeyondPageLimitWithMatchingTotals(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "selection-owner"
	want := make([]string, 205)
	for index := range want {
		want[index] = fmt.Sprintf("selection-%03d", index)
		seedLiteratureCollectionItem(t, store, owner, want[index], "Matching reference")
	}
	seedLiteratureCollectionItem(t, store, "another-owner", "selection-foreign", "Matching reference")
	seedLiteratureCollectionItem(t, store, owner, "selection-deleted", "Matching reference")
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), owner, "selection-deleted", LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted,
	}); err != nil {
		t.Fatal(err)
	}
	selection, err := store.ListLiteratureItemIDs(t.Context(), owner, "matching", "", "", "")
	if err != nil || selection.TotalCount != int64(len(want)) || !reflect.DeepEqual(selection.ItemIDs, want) {
		t.Fatalf("complete selection was paginated or leaked another owner/deleted item: %+v %v", selection, err)
	}
	var paged []string
	cursor := ""
	for pageIndex := 0; pageIndex < 3; pageIndex++ {
		page, err := store.ListLiteratureItems(t.Context(), owner, "matching", "", cursor, 100)
		if err != nil || page.TotalCount == nil || *page.TotalCount != selection.TotalCount {
			t.Fatalf("page count was its page length or a different filter: %+v %v", page, err)
		}
		for _, item := range page.Items {
			paged = append(paged, item.ID)
		}
		cursor = page.NextCursor
	}
	if cursor != "" || !reflect.DeepEqual(paged, selection.ItemIDs) {
		t.Fatalf("full ID order differs from ordinary pages: %v cursor%q", paged, cursor)
	}
	missing, err := store.ListLiteratureItemIDs(t.Context(), owner, "no matching title", "", "", "")
	if err != nil || missing.ItemIDs == nil || len(missing.ItemIDs) != 0 || missing.TotalCount != 0 {
		t.Fatalf("empty projection is not a complete empty list: %+v %v", missing, err)
	}
	raw, err := json.Marshal(missing)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 3 || string(fields["item_ids"]) != "[]" ||
		string(fields["schema"]) != `"x-science.literature-selection.v1"` {
		t.Fatalf("selection wire envelope is not closed: %s %v", raw, err)
	}
}

func TestLiteratureSelectionSharesScopedFiltersAndCollectionOrder(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner, project = "scoped-selection-owner", "scoped-selection-project"
	collection := mustCreateLiteratureCollection(t, store, owner, "Selection subset", "")
	if _, err := store.CreateProject(CreateProjectInput{ID: project, UserID: owner, Name: "Selection project"}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, title        string
		member, included bool
		status           LiteratureReadingStatus
	}{
		{"z-first", "Keep first", true, true, LiteratureRead},
		{"a-second", "Keep second", true, true, LiteratureRead},
		{"m-other-title", "Other title", true, true, LiteratureRead},
		{"b-other-state", "Keep unread", true, true, LiteratureUnread},
		{"c-other-project", "Keep other project", true, false, LiteratureRead},
		{"d-outside-collection", "Keep outside", false, true, LiteratureRead},
	} {
		seedLiteratureCollectionItem(t, store, owner, item.id, item.title)
		if item.member {
			if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, item.id, uuid.NewString(), true); err != nil {
				t.Fatal(err)
			}
		}
		if item.included {
			if _, err := store.SetLiteratureProject(t.Context(), owner, item.id, project, uuid.NewString(), true); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.SetLiteratureReadingStatus(t.Context(), owner, LiteratureReadingStatusInput{
			OperationID: uuid.NewString(), ItemIDs: []string{item.id}, ReadingStatus: item.status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	selection, err := store.ListLiteratureItemIDs(t.Context(), owner, "kEeP", project, collection.CollectionID, LiteratureRead)
	want := []string{"z-first", "a-second"}
	if err != nil || !reflect.DeepEqual(selection.ItemIDs, want) || selection.TotalCount != 2 {
		t.Fatalf("scope predicates or collection order differ: %+v %v", selection, err)
	}
	first, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "kEeP", project, collection.CollectionID, LiteratureRead, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != want[0] || first.TotalCount == nil || *first.TotalCount != 2 {
		t.Fatalf("filtered page count/order differs: %+v %v", first, err)
	}
	second, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "kEeP", project, collection.CollectionID, LiteratureRead, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != want[1] || second.TotalCount == nil || *second.TotalCount != 2 {
		t.Fatalf("resumed page count/order differs: %+v %v", second, err)
	}
	// A JSON array is legal SQLite JSON but not valid item metadata. The ID-only
	// query must not hydrate it, unlike the actual item reader.
	if _, err := store.db.Exec(`UPDATE literature_items SET metadata_json='[]' WHERE id=?`, want[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLiteratureItem(t.Context(), owner, want[0]); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("fixture did not distinguish metadata hydration: %v", err)
	}
	idsOnly, err := store.ListLiteratureItemIDs(t.Context(), owner, "kEeP", project, collection.CollectionID, LiteratureRead)
	if err != nil || !reflect.DeepEqual(idsOnly.ItemIDs, want) {
		t.Fatalf("selection hydrated metadata instead of IDs: %+v %v", idsOnly, err)
	}
}

func TestLiteratureSelectionRejectsCancelledOrUnavailableReadsWithoutPartialIDs(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "failed-selection-owner"
	seedLiteratureCollectionItem(t, store, owner, "available-reference", "Available reference")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	selection, err := store.ListLiteratureItemIDs(cancelled, owner, "", "", "", "")
	if !errors.Is(err, context.Canceled) || len(selection.ItemIDs) != 0 || selection.TotalCount != 0 {
		t.Fatalf("cancelled read returned partial success: %+v %v", selection, err)
	}
	foreign := mustCreateLiteratureCollection(t, store, "foreign-selection-owner", "Foreign selection", "")
	for _, input := range []struct {
		collection string
		status     LiteratureReadingStatus
		want       error
	}{
		{"missing-collection", "", ErrLiteratureUnavailable},
		{foreign.CollectionID, "", ErrLiteratureUnavailable},
		{"", LiteratureReadingStatus("archived"), ErrLiteratureInvalid},
	} {
		result, err := store.ListLiteratureItemIDs(t.Context(), owner, "", "", input.collection, input.status)
		if !errors.Is(err, input.want) || len(result.ItemIDs) != 0 || result.TotalCount != 0 {
			t.Fatalf("invalid scope produced a partial list: %+v %v", result, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	selection, err = store.ListLiteratureItemIDs(t.Context(), owner, "", "", "", "")
	if err == nil || len(selection.ItemIDs) != 0 || selection.TotalCount != 0 {
		t.Fatalf("closed database returned a partial list: %+v %v", selection, err)
	}
}
