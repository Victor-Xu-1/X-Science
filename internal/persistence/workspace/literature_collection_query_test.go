package workspace

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureCollectionPagesBindScopeAndOriginalCursor(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "page-owner"
	collection := mustCreateLiteratureCollection(t, store, owner, "A", "")
	other := mustCreateLiteratureCollection(t, store, owner, "B", "")
	for _, item := range []struct{ id, title string }{{"z-first", "Alpha"}, {"a-second", "Beta"}} {
		seedLiteratureCollectionItem(t, store, owner, item.id, item.title)
		if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, item.id, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", collection.CollectionID, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "z-first" || first.NextCursor == "" {
		t.Fatalf("first member page=%+v err=%v", first, err)
	}
	second, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", collection.CollectionID, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "a-second" || second.NextCursor != "" {
		t.Fatalf("second member page=%+v err=%v", second, err)
	}
	for _, field := range []string{"Collection", "Owner"} {
		raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", collection.CollectionID, base64.RawURLEncoding.EncodeToString(raw), 1); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("omitted cursor %s borrowed request scope: %v", field, err)
		}
	}
	for _, scope := range []struct{ owner, query, project, collection string }{
		{owner, "", "", other.CollectionID}, {owner, "Alpha", "", collection.CollectionID},
		{owner, "", "project", collection.CollectionID}, {"another-owner", "", "", collection.CollectionID},
		{owner, "", "", ""},
	} {
		if _, err := store.ListLiteratureItemsInCollection(t.Context(), scope.owner, scope.query, scope.project, scope.collection, first.NextCursor, 1); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("borrowed cursor accepted for %+v: %v", scope, err)
		}
	}
	raw, err := json.Marshal(struct{ After, Query, Project, Owner string }{"a-second", "", "", owner})
	if err != nil {
		t.Fatal(err)
	}
	legacy := base64.RawURLEncoding.EncodeToString(raw)
	if page, err := store.ListLiteratureItems(t.Context(), owner, "", "", legacy, 1); err != nil || len(page.Items) != 1 || page.Items[0].ID != "z-first" {
		t.Fatalf("original unscoped cursor lost: %+v %v", page, err)
	}
	if _, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", collection.CollectionID, legacy, 1); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("old unscoped cursor borrowed for collection: %v", err)
	}
	if _, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", "missing", "", 20); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("missing collection became empty page: %v", err)
	}
	foreign := mustCreateLiteratureCollection(t, store, "another-owner", "Foreign", "")
	if _, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", foreign.CollectionID, "", 20); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign collection became empty page: %v", err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "project", UserID: owner, Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLiteratureProject(t.Context(), owner, "a-second", "project", uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListLiteratureItemsInCollection(t.Context(), owner, "Beta", "project", collection.CollectionID, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "a-second" {
		t.Fatalf("conjunctive filters=%+v err=%v", page, err)
	}
	page, err = store.ListLiteratureItemsInCollection(t.Context(), owner, "Alpha", "project", collection.CollectionID, "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("collection/query/project became OR: %+v %v", page, err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", uuid.NewString(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	page, err = store.ListLiteratureItemsInCollection(t.Context(), owner, "", "", collection.CollectionID, "", 20)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "a-second" || page.Items[1].ID != "z-first" {
		t.Fatalf("re-added member did not append at tail: %+v %v", page, err)
	}
}
