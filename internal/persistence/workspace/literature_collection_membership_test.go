package workspace

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureCollectionMembershipPreservesOrderAndOwnership(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "member-owner"
	collection := mustCreateLiteratureCollection(t, store, owner, "Members", "")
	seedLiteratureCollectionItem(t, store, owner, "z-first", "First")
	seedLiteratureCollectionItem(t, store, owner, "a-second", "Second")
	seedLiteratureCollectionItem(t, store, "another-owner", "foreign-item", "Foreign")
	firstOperation := uuid.NewString()
	first, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", firstOperation, true)
	if err != nil || first.Included == nil || !*first.Included || first.Collection != nil {
		t.Fatalf("membership=%+v err=%v", first, err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "a-second", uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	position := func() string {
		var value string
		if err := store.db.QueryRow(`SELECT printf('%d:%s',sort_order,hex(CAST(added_at AS BLOB))) FROM literature_collection_items WHERE collection_id=? AND item_id='z-first'`, collection.CollectionID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := position()
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", uuid.NewString(), true); err != nil || position() != before {
		t.Fatalf("duplicate include reordered original member: %v", err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "foreign-item", uuid.NewString(), true); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign member accepted: %v", err)
	}
	foreign := mustCreateLiteratureCollection(t, store, "another-owner", "Foreign collection", "")
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, foreign.CollectionID, "a-second", uuid.NewString(), true); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign collection accepted: %v", err)
	}
	// The projection independently fences owner even if an invalid old join
	// exists. This is isolated fixture data, not a production mutation path.
	if _, err := store.db.Exec(`INSERT INTO literature_collection_items(collection_id,item_id,sort_order,added_at) VALUES(?,'foreign-item',2,'2026-01-01')`, collection.CollectionID); err != nil {
		t.Fatal(err)
	}
	current, err := readLiteratureCollection(t.Context(), store.readDatabase(), owner, collection.CollectionID)
	if err != nil || current.ItemCount != 2 || current.Revision != 1 {
		t.Fatalf("live owned direct count/revision=%+v err=%v", current, err)
	}
	membership, err := store.GetLiteratureItemCollections(t.Context(), owner, "z-first")
	if err != nil || !reflect.DeepEqual(membership.CollectionIDs, []string{collection.CollectionID}) {
		t.Fatalf("membership read=%+v err=%v", membership, err)
	}
	deleted := true
	if _, err := store.PatchLiteratureItem(t.Context(), owner, "z-first", LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLiteratureItemCollections(t.Context(), owner, "z-first"); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("deleted selection projected as empty: %v", err)
	}
	if _, err := store.GetLiteratureItemCollections(t.Context(), owner, "foreign-item"); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign selection projected as empty: %v", err)
	}
	removed, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", uuid.NewString(), false)
	if err != nil || removed.Included == nil || *removed.Included {
		t.Fatalf("owned tombstone unlink=%+v err=%v", removed, err)
	}
	replayed, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", firstOperation, true)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("original receipt blocked by later tombstone: %+v %v", replayed, err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", firstOperation, false); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed original member command=%v", err)
	}
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, collection.CollectionID, "z-first", uuid.NewString(), true); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("fresh add revived tombstone: %v", err)
	}
	current, err = readLiteratureCollection(t.Context(), store.readDatabase(), owner, collection.CollectionID)
	if err != nil || current.ItemCount != 1 || current.Revision != 1 {
		t.Fatalf("membership changed metadata revision/count: %+v %v", current, err)
	}
}
