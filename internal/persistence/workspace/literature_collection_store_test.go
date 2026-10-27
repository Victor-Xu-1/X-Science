package workspace

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func literatureCollectionStoreFixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collections.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func mustCreateLiteratureCollection(t *testing.T, store *Store, owner, name, parent string) LiteratureCollectionReceipt {
	t.Helper()
	receipt, err := store.CreateLiteratureCollection(t.Context(), owner,
		LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: name, ParentID: parent})
	if err != nil || receipt.Collection == nil {
		t.Fatalf("create=%+v err=%v", receipt, err)
	}
	return receipt
}

func seedLiteratureCollectionItem(t *testing.T, store *Store, owner, id, title string) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at)
 VALUES(?,?,?,'{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, owner, title); err != nil {
		t.Fatal(err)
	}
}

func TestLiteratureCollectionsHierarchyReceiptsAndReopen(t *testing.T) {
	store, path := literatureCollectionStoreFixture(t)
	const owner = "collection-owner"
	parent := mustCreateLiteratureCollection(t, store, owner, "Parent", "")
	childInput := LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "  Ｒｅｖｉｅｗ\u00a0Notes ", Description: "  saved\n description  ", ParentID: parent.CollectionID}
	child, err := store.CreateLiteratureCollection(t.Context(), owner, childInput)
	if err != nil || child.Collection.Name != "Review Notes" || child.Collection.Description != "saved description" || child.Collection.SortOrder != 0 {
		t.Fatalf("child=%+v err=%v", child, err)
	}
	sibling := mustCreateLiteratureCollection(t, store, owner, "Sibling", parent.CollectionID)
	if sibling.Collection.SortOrder != 1 {
		t.Fatal("create did not append sibling order")
	}
	if _, err := store.CreateLiteratureCollection(t.Context(), owner, LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "review notes", ParentID: parent.CollectionID}); !errors.Is(err, ErrLiteratureCollectionNameConflict) {
		t.Fatalf("sibling name conflict=%v", err)
	}
	otherParent := mustCreateLiteratureCollection(t, store, owner, "Other parent", "")
	_ = mustCreateLiteratureCollection(t, store, owner, "Review Notes", otherParent.CollectionID)
	foreign := mustCreateLiteratureCollection(t, store, "other-owner", "Foreign", "")
	if _, err := store.CreateLiteratureCollection(t.Context(), owner, LiteratureCollectionCreate{OperationID: uuid.NewString(), Name: "Blocked", ParentID: foreign.CollectionID}); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign parent accepted: %v", err)
	}
	update := LiteratureCollectionUpdate{OperationID: uuid.NewString(), ExpectedRevision: 1, Name: "Updated", Description: "new description"}
	updated, err := store.UpdateLiteratureCollection(t.Context(), owner, child.CollectionID, update)
	if err != nil || updated.Collection.Revision != 2 || updated.Collection.ParentID == nil || *updated.Collection.ParentID != parent.CollectionID {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	if _, err := store.UpdateLiteratureCollection(t.Context(), owner, child.CollectionID, LiteratureCollectionUpdate{OperationID: uuid.NewString(), ExpectedRevision: 1, Name: "Stale"}); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	changed := update
	changed.Name = "Changed request"
	if _, err := store.UpdateLiteratureCollection(t.Context(), owner, child.CollectionID, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed original request=%v", err)
	}
	seedLiteratureCollectionItem(t, store, owner, "retained-paper", "A retained reference")
	if _, err := store.SetLiteratureCollectionItem(t.Context(), owner, parent.CollectionID, "retained-paper", uuid.NewString(), true); err != nil {
		t.Fatal(err)
	}
	deleteID := uuid.NewString()
	deleted, err := store.DeleteLiteratureCollection(t.Context(), owner, parent.CollectionID, deleteID)
	if err != nil || deleted.Action != "delete" || deleted.Collection != nil || deleted.ItemID != "" {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	promoted, err := readLiteratureCollection(t.Context(), store.readDatabase(), owner, child.CollectionID)
	if err != nil || promoted.ParentID != nil || promoted.Revision != 3 {
		t.Fatalf("promotion=%+v err=%v", promoted, err)
	}
	if item, err := store.GetLiteratureItem(t.Context(), owner, "retained-paper"); err != nil || item.Deleted {
		t.Fatalf("collection deletion removed paper: %+v %v", item, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, replay := range []struct {
		expected LiteratureCollectionReceipt
		run      func() (LiteratureCollectionReceipt, error)
	}{
		{child, func() (LiteratureCollectionReceipt, error) {
			return reopened.CreateLiteratureCollection(t.Context(), owner, childInput)
		}},
		{updated, func() (LiteratureCollectionReceipt, error) {
			return reopened.UpdateLiteratureCollection(t.Context(), owner, child.CollectionID, update)
		}},
		{deleted, func() (LiteratureCollectionReceipt, error) {
			return reopened.DeleteLiteratureCollection(t.Context(), owner, parent.CollectionID, deleteID)
		}},
	} {
		got, err := replay.run()
		if err != nil || !reflect.DeepEqual(got, replay.expected) {
			t.Fatalf("frozen replay changed: got=%+v want=%+v err=%v", got, replay.expected, err)
		}
	}
	page, err := reopened.ListLiteratureCollections(t.Context(), owner)
	if err != nil || len(page.Collections) != 4 {
		t.Fatalf("owned complete tree=%+v err=%v", page, err)
	}
}

func TestLiteratureCollectionPromotionCollisionRollsBack(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	const owner = "owner"
	_ = mustCreateLiteratureCollection(t, store, owner, "Shared", "")
	parent := mustCreateLiteratureCollection(t, store, owner, "Parent", "")
	child := mustCreateLiteratureCollection(t, store, owner, "SHARED", parent.CollectionID)
	operation := uuid.NewString()
	if _, err := store.DeleteLiteratureCollection(t.Context(), owner, parent.CollectionID, operation); !errors.Is(err, ErrLiteratureCollectionNameConflict) {
		t.Fatalf("promotion conflict=%v", err)
	}
	still, err := readLiteratureCollection(t.Context(), store.readDatabase(), owner, child.CollectionID)
	if err != nil || !reflect.DeepEqual(still, *child.Collection) {
		t.Fatalf("failed promotion changed child: %+v %v", still, err)
	}
	if _, err := readLiteratureCollection(t.Context(), store.readDatabase(), owner, parent.CollectionID); err != nil {
		t.Fatal("failed promotion removed parent")
	}
	var receipts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation='literature.collection.delete'`, owner, operation).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed mutation saved receipt: %d %v", receipts, err)
	}
	if _, err := store.UpdateLiteratureCollection(t.Context(), owner, child.CollectionID, LiteratureCollectionUpdate{OperationID: uuid.NewString(), ExpectedRevision: 1, Name: "Unique"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteLiteratureCollection(t.Context(), owner, parent.CollectionID, operation); err != nil {
		t.Fatalf("failed attempt poisoned original operation: %v", err)
	}
}
