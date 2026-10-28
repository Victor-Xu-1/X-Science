package workspace

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureNoteSearchMatchesBeforePagingAndSelection(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "note-search-owner"
	for _, item := range []struct{ id, title, note string }{
		{"a-unrelated", "Unrelated reference", "Other text"},
		{"b-note", "A reference", "\ufeffＦＩＲＳＴ\u00a0\tＮＯＴＥ\ufeff"},
		{"c-both", "First note in the title", "First note in the body"},
		{"d-note", "Another reference", "An original first note suffix"},
	} {
		seedLiteratureCollectionItem(t, store, owner, item.id, item.title)
		patch, err := json.Marshal(map[string]any{"set": map[string]any{"personal_note": item.note}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PatchLiteratureItem(t.Context(), owner, item.id, LiteraturePatch{
			OperationID: uuid.NewString(), ExpectedRevision: 1, MetadataPatch: patch,
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"b-note", "c-both", "d-note"}
	var got []string
	cursor := ""
	for range want {
		page, err := store.ListLiteratureItems(t.Context(), owner, "first note", "", cursor, 1)
		if err != nil || len(page.Items) != 1 || page.TotalCount == nil || *page.TotalCount != 3 {
			t.Fatalf("saved personal notes must match before paging/count: page=%+v err=%v", page, err)
		}
		got = append(got, page.Items[0].ID)
		cursor = page.NextCursor
	}
	selection, err := store.ListLiteratureItemIDs(t.Context(), owner, "first note", "", "", "")
	if err != nil || cursor != "" || !reflect.DeepEqual(got, want) ||
		!reflect.DeepEqual(selection.ItemIDs, want) || selection.TotalCount != 3 {
		t.Fatalf("note page/count/all-match order differs: pageIDs=%v cursor=%q selection=%+v err=%v", got, cursor, selection, err)
	}
	item, err := store.GetLiteratureItem(t.Context(), owner, "b-note")
	if err != nil || item.Metadata["personal_note"] != "\ufeffＦＩＲＳＴ\u00a0\tＮＯＴＥ\ufeff" || item.MetadataRevision != 2 {
		t.Fatalf("search changed the original metadata/revision: %+v %v", item, err)
	}
}

func TestLiteratureNoteSearchRetainsConjunctiveScopesAndCursor(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner, project = "scoped-note-owner", "scoped-note-project"
	collection := mustCreateLiteratureCollection(t, store, owner, "Scoped notes", "")
	if _, err := store.CreateProject(CreateProjectInput{ID: project, UserID: owner, Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, note                  string
		member, included, deleted bool
		status                    LiteratureReadingStatus
	}{
		{"z-first", "Needle", true, true, false, LiteratureRead},
		{"a-second", "ＮＥＥＤＬＥ", true, true, false, LiteratureRead},
		{"b-unread", "Needle", true, true, false, LiteratureUnread},
		{"c-other-project", "Needle", true, false, false, LiteratureRead},
		{"d-outside-collection", "Needle", false, true, false, LiteratureRead},
		{"e-deleted", "Needle", true, true, true, LiteratureRead},
		{"f-other-note", "Unrelated text", true, true, false, LiteratureRead},
	} {
		seedLiteratureCollectionItem(t, store, owner, item.id, "A reference")
		setLiteratureQueryNote(t, store, owner, item.id, item.note)
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
		if item.deleted {
			deleted := true
			if _, err := store.PatchLiteratureItem(t.Context(), owner, item.id, LiteraturePatch{
				OperationID: uuid.NewString(), ExpectedRevision: 2, Deleted: &deleted,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedLiteratureCollectionItem(t, store, "foreign-owner", "foreign-note", "Needle")
	setLiteratureQueryNote(t, store, "foreign-owner", "foreign-note", "Needle")
	want := []string{"z-first", "a-second"}
	selection, err := store.ListLiteratureItemIDs(t.Context(), owner, "needle", project, collection.CollectionID, LiteratureRead)
	if err != nil || selection.TotalCount != 2 || !reflect.DeepEqual(selection.ItemIDs, want) {
		t.Fatalf("note query bypassed a scope or collection order: %+v %v", selection, err)
	}
	first, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "needle", project, collection.CollectionID, LiteratureRead, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != want[0] || first.NextCursor == "" || first.TotalCount == nil || *first.TotalCount != 2 {
		t.Fatalf("first scoped note page=%+v err=%v", first, err)
	}
	second, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "needle", project, collection.CollectionID, LiteratureRead, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != want[1] || second.NextCursor != "" || second.TotalCount == nil || *second.TotalCount != 2 {
		t.Fatalf("second scoped note page=%+v err=%v", second, err)
	}
	if _, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "ＮＥＥＤＬＥ", project, collection.CollectionID, LiteratureRead, first.NextCursor, 1); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("equivalent normalized text borrowed another raw-query cursor: %v", err)
	}
	all, err := store.ListLiteratureItemIDs(t.Context(), owner, "needle", "", "", "")
	if err != nil || all.TotalCount != 5 {
		t.Fatalf("unscoped note query included foreign/deleted/nonmatching items: %+v %v", all, err)
	}
}

func TestLiteratureNoteSearchUsesStringValuesAndLiteralUnicodeSubstrings(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "literal-note-owner"
	for _, item := range []struct {
		id, title string
		note      any
	}{
		{"text", "Reference", "100%_ O'Brien"},
		{"object", "Reference", map[string]any{"token": "100%_ O'Brien"}},
		{"array", "Reference", []any{"100%_ O'Brien"}},
		{"number", "Reference", 100},
		{"boolean", "Reference", true},
		{"null", "Reference", nil},
		{"final", "ΟΣ", "No note match"},
		{"medial", "Reference", "ΟΣΑ"},
	} {
		seedLiteratureCollectionItem(t, store, owner, item.id, item.title)
		setLiteratureQueryNote(t, store, owner, item.id, item.note)
	}
	seedLiteratureCollectionItem(t, store, owner, "absent", "Reference")
	for _, example := range []struct {
		query string
		want  []string
	}{
		{"100%_ O'Brien", []string{"text"}},
		{"%_", []string{"text"}},
		{"token", []string{}},
		{"100", []string{"text"}},
		{"true", []string{}},
		{"ος", []string{"final"}},
		{"οσ", []string{"medial"}},
	} {
		selection, err := store.ListLiteratureItemIDs(t.Context(), owner, example.query, "", "", "")
		if err != nil || !reflect.DeepEqual(selection.ItemIDs, example.want) || selection.TotalCount != int64(len(example.want)) {
			t.Fatalf("query %q coerced opaque metadata, folded case or interpreted SQL syntax: %+v %v", example.query, selection, err)
		}
	}
	selection, err := store.ListLiteratureItemIDs(t.Context(), owner, "\ufeff\t\u00a0", "", "", "")
	if err != nil || selection.TotalCount != 9 {
		t.Fatalf("normalized blank query lost original empty-query scope: %+v %v", selection, err)
	}
}

func setLiteratureQueryNote(t *testing.T, store *Store, owner, id string, note any) {
	t.Helper()
	patch, err := json.Marshal(map[string]any{"set": map[string]any{"personal_note": note}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), owner, id, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1, MetadataPatch: patch,
	}); err != nil {
		t.Fatal(err)
	}
}
