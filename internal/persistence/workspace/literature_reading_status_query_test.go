package workspace

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureReadingStatusFiltersScopedPagesAndBindsLegacyCursor(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = readingStatusTestOwner
	ids := []string{"status-query-a", "status-query-b", "status-query-c", "status-query-d"}
	titles := []string{"Keep unread", "Keep reading", "Other read", "Keep read"}
	for index, id := range ids {
		seedLiteratureCollectionItem(t, store, owner, id, titles[index])
	}
	for _, input := range []LiteratureReadingStatusInput{
		{OperationID: uuid.NewString(), ItemIDs: ids[2:], ReadingStatus: LiteratureRead},
		{OperationID: uuid.NewString(), ItemIDs: ids[1:2], ReadingStatus: LiteratureReading},
	} {
		if _, err := store.SetLiteratureReadingStatus(t.Context(), owner, input); err != nil {
			t.Fatal(err)
		}
	}
	seedLiteratureCollectionItem(t, store, "foreign-status-owner", "status-query-foreign", "Keep foreign")
	if _, err := store.SetLiteratureReadingStatus(t.Context(), "foreign-status-owner", LiteratureReadingStatusInput{
		OperationID: uuid.NewString(), ItemIDs: []string{"status-query-foreign"}, ReadingStatus: LiteratureRead,
	}); err != nil {
		t.Fatal(err)
	}
	seedLiteratureCollectionItem(t, store, owner, "status-query-deleted", "Keep deleted")
	if _, err := store.SetLiteratureReadingStatus(t.Context(), owner, LiteratureReadingStatusInput{
		OperationID: uuid.NewString(), ItemIDs: []string{"status-query-deleted"}, ReadingStatus: LiteratureRead,
	}); err != nil {
		t.Fatal(err)
	}
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), owner, "status-query-deleted", LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted,
	}); err != nil {
		t.Fatal(err)
	}
	collection := mustCreateLiteratureCollection(t, store, owner, "Reading subset", "")
	for _, id := range []string{ids[1], ids[3]} {
		if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, id, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
	}
	const project = "status-query-project"
	if _, err := store.CreateProject(CreateProjectInput{ID: project, UserID: owner, Name: "Reading project"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[2:] {
		if _, err := store.SetLiteratureProject(t.Context(), owner, id, project, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
	}
	itemIDs := func(page LiteraturePage) []string {
		values := make([]string, len(page.Items))
		for index, item := range page.Items {
			values[index] = item.ID
		}
		return values
	}
	for _, tc := range []struct {
		name, query, project, collection string
		status                           LiteratureReadingStatus
		want                             []string
	}{
		{"all", "", "", "", "", ids},
		{"unread", "", "", "", LiteratureUnread, ids[:1]},
		{"reading", "", "", "", LiteratureReading, ids[1:2]},
		{"read", "", "", "", LiteratureRead, ids[2:]},
		{"query", "Keep", "", "", LiteratureRead, ids[3:]},
		{"project", "", project, "", LiteratureRead, ids[2:]},
		{"collection", "", "", collection.CollectionID, LiteratureRead, ids[3:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, tc.query, tc.project, tc.collection, tc.status, "", 20)
			if err != nil || !reflect.DeepEqual(itemIDs(page), tc.want) {
				t.Fatalf("wrong status-filtered scope: %+v %v want%v", page, err, tc.want)
			}
			for _, item := range page.Items {
				if !validLiteratureReadingStatus(item.ReadingStatus) || tc.status != "" && item.ReadingStatus != tc.status {
					t.Fatalf("stored state was not projected: %+v", item)
				}
			}
		})
	}
	first, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", "", "", 2)
	legacy, encodeErr := json.Marshal(struct{ After, Query, Project, Owner string }{ids[1], "", "", owner})
	if err != nil || encodeErr != nil || first.NextCursor != base64.RawURLEncoding.EncodeToString(legacy) {
		t.Fatalf("unfiltered four-field legacy cursor changed: %q %v %v", first.NextCursor, err, encodeErr)
	}
	second, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", "", first.NextCursor, 2)
	if err != nil || !reflect.DeepEqual(itemIDs(second), ids[2:]) {
		t.Fatal("legacy cursor no longer resumes the original scope", second, err)
	}
	filtered, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "", "", "", LiteratureRead, "", 1)
	if err != nil || !reflect.DeepEqual(itemIDs(filtered), ids[2:3]) || filtered.NextCursor == "" {
		t.Fatal("status was applied after pagination", filtered, err)
	}
	for _, tc := range []struct {
		status LiteratureReadingStatus
		cursor string
	}{
		{LiteratureRead, first.NextCursor},
		{LiteratureUnread, filtered.NextCursor},
		{"", filtered.NextCursor},
		{LiteratureReadingStatus("archived"), ""},
	} {
		if _, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "", "", "", tc.status, tc.cursor, 1); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("cursor/filter mismatch escaped validation: %+v %v", tc, err)
		}
	}
	if _, err := store.SetLiteratureReadingStatus(t.Context(), owner, LiteratureReadingStatusInput{
		OperationID: uuid.NewString(), ItemIDs: ids[2:3], ReadingStatus: LiteratureUnread,
	}); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ListLiteratureItemsWithReadingStatus(t.Context(), owner, "", "", "", LiteratureRead, filtered.NextCursor, 1)
	if err != nil || !reflect.DeepEqual(itemIDs(remaining), ids[3:]) {
		t.Fatal("status change skipped the remaining cursor member", remaining, err)
	}
}
