package workspace

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func resourceTagsFixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tags.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func resourceTagsCreate(t *testing.T, store *Store, owner, name string) ResourceTagReceipt {
	t.Helper()
	receipt, err := store.CreateResourceTag(t.Context(), owner, ResourceTagCreate{
		OperationID: uuid.NewString(), Name: name, IconKey: "tag", ColorKey: "blue",
	})
	if err != nil || receipt.TagID == "" {
		t.Fatalf("create=%+v err=%v", receipt, err)
	}
	return receipt
}

func resourceTagsRead(t *testing.T, store *Store, owner string) ResourceTagSnapshot {
	t.Helper()
	snapshot, err := store.ListResourceTags(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func resourceTagsCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	// Only the fixed test table names below are accepted by this helper.
	switch table {
	case "resource_tag_catalog_state", "resource_tags", "resource_tag_assignments", "mutation_result_ledger":
	default:
		t.Fatal("unexpected count table")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestResourceTagsReadOnlyAndDurableReceipts(t *testing.T) {
	store, path := resourceTagsFixture(t)
	const owner = "tag-owner"
	for _, account := range []string{owner, "other-owner"} {
		got := resourceTagsRead(t, store, account)
		if got.Schema != resourceTagSnapshotSchema || got.Revision != 0 || len(got.Tags) != 1 ||
			got.Tags[0].ID != ResourceFavoriteTagID || got.Tags[0].SystemKey != "favorite" ||
			got.Tags[0].CreatedAt != 0 || got.Tags[0].UpdatedAt != 0 || got.Assignments == nil ||
			!reflect.DeepEqual(got.ResourceTypes, []string{"literature.item"}) {
			t.Fatalf("virtual catalog=%+v", got)
		}
	}
	if resourceTagsCount(t, store, "resource_tag_catalog_state") != 0 || resourceTagsCount(t, store, "resource_tags") != 0 {
		t.Fatal("GET initialized persistent state")
	}
	input := ResourceTagCreate{OperationID: "durable-create", Name: "  Ｒｅｖｉｅｗ\u00a0Notes ", IconKey: "tag", ColorKey: "blue"}
	created, err := store.CreateResourceTag(t.Context(), owner, input)
	if err != nil || created.Action != "create" || created.Snapshot.Revision != 1 || created.Snapshot.Tags[1].Name != "Review Notes" || created.Snapshot.Tags[0].CreatedAt <= 0 {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	changed := input
	changed.Name = "Different"
	if _, err := store.CreateResourceTag(t.Context(), owner, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed request=%v", err)
	}
	other := resourceTagsCreate(t, store, "other-owner", "Review Notes")
	if len(other.Snapshot.Tags) != 2 || other.Snapshot.Tags[0].ID != ResourceFavoriteTagID || len(resourceTagsRead(t, store, owner).Tags) != 2 {
		t.Fatal("account catalogs were shared")
	}
	if _, err := store.DeleteResourceTag(t.Context(), owner, created.TagID, "durable-delete"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := reopened.CreateResourceTag(t.Context(), owner, input)
	if err != nil || !reflect.DeepEqual(replay, created) {
		t.Fatalf("receipt after reopen=%+v err=%v", replay, err)
	}
	if current := resourceTagsRead(t, reopened, owner); len(current.Tags) != 1 || current.Revision != 2 {
		t.Fatalf("receipt resurrected deleted tag: %+v", current)
	}
	var absent *Store
	if _, err := absent.ListResourceTags(t.Context(), owner); err == nil {
		t.Fatal("nil store accepted")
	}
}

func TestResourceTagsReceiptFailureRollsBackWholeMutation(t *testing.T) {
	store, _ := resourceTagsFixture(t)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_tag_receipt BEFORE INSERT ON mutation_result_ledger
 WHEN NEW.operation='resource.tag.create' BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	input := ResourceTagCreate{OperationID: "receipt-failure", Name: "Notes", IconKey: "tag", ColorKey: "blue"}
	if _, err := store.CreateResourceTag(t.Context(), "owner", input); err == nil {
		t.Fatal("injected receipt failure accepted")
	}
	for _, table := range []string{"resource_tag_catalog_state", "resource_tags", "mutation_result_ledger"} {
		if resourceTagsCount(t, store, table) != 0 {
			t.Fatalf("failed transaction retained %s", table)
		}
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_tag_receipt`); err != nil {
		t.Fatal(err)
	}
	if got, err := store.CreateResourceTag(t.Context(), "owner", input); err != nil || got.Snapshot.Revision != 1 || len(got.Snapshot.Tags) != 2 {
		t.Fatalf("retry=%+v err=%v", got, err)
	}
}

func TestResourceTagsReorderDeleteAndProtectedSystem(t *testing.T) {
	store, _ := resourceTagsFixture(t)
	const owner = "ordered-owner"
	if _, err := store.ReorderResourceTags(t.Context(), owner, ResourceTagOrder{OperationID: "missing-order"}); !errors.Is(err, ErrResourceTagInvalid) {
		t.Fatalf("nil order=%v", err)
	}
	noop, err := store.ReorderResourceTags(t.Context(), owner, ResourceTagOrder{OperationID: "empty-order", TagIDs: []string{}})
	if err != nil || noop.Snapshot.Revision != 0 || resourceTagsCount(t, store, "mutation_result_ledger") != 1 {
		t.Fatalf("empty explicit order=%+v err=%v", noop, err)
	}
	a := resourceTagsCreate(t, store, owner, "A").TagID
	b := resourceTagsCreate(t, store, owner, "B").TagID
	c := resourceTagsCreate(t, store, owner, "C").TagID
	for index, ids := range [][]string{{a}, {a, a, c}, {a, b, ResourceFavoriteTagID}} {
		if _, err := store.ReorderResourceTags(t.Context(), owner, ResourceTagOrder{OperationID: uuid.NewString(), TagIDs: ids}); err == nil {
			t.Fatalf("invalid order %d accepted", index)
		}
	}
	reordered, err := store.ReorderResourceTags(t.Context(), owner, ResourceTagOrder{OperationID: "reverse", TagIDs: []string{c, b, a}})
	if err != nil || reordered.Snapshot.Revision != 4 || reordered.Snapshot.Tags[1].ID != c || reordered.Snapshot.Tags[3].ID != a {
		t.Fatalf("reorder=%+v err=%v", reordered, err)
	}
	same, err := store.ReorderResourceTags(t.Context(), owner, ResourceTagOrder{OperationID: "same-order", TagIDs: []string{c, b, a}})
	if err != nil || same.Snapshot.Revision != reordered.Snapshot.Revision {
		t.Fatalf("same order changed revision: %+v err=%v", same, err)
	}
	deleted, err := store.DeleteResourceTag(t.Context(), owner, b, "delete-middle")
	if err != nil || deleted.Snapshot.Revision != 5 || len(deleted.Snapshot.Tags) != 3 || deleted.Snapshot.Tags[1].ID != c || deleted.Snapshot.Tags[2].ID != a {
		t.Fatalf("delete=%+v err=%v", deleted, err)
	}
	rows, err := store.db.Query(`SELECT sort_order FROM resource_tags WHERE owner_user_id=? ORDER BY sort_order`, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var order int
		if err := rows.Scan(&order); err != nil || order != index {
			t.Fatalf("noncontiguous order=%d expected=%d err=%v", order, index, err)
		}
		index++
	}
	if err := rows.Err(); err != nil || index != 3 {
		t.Fatalf("order rows=%d err=%v", index, err)
	}
	_ = rows.Close()
	if _, err := store.DeleteResourceTag(t.Context(), owner, ResourceFavoriteTagID, "delete-favorite"); !errors.Is(err, ErrResourceTagProtected) {
		t.Fatalf("favorite deletion=%v", err)
	}
	if _, err := store.UpdateResourceTag(t.Context(), owner, ResourceFavoriteTagID, ResourceTagUpdate{OperationID: "edit-favorite"}); !errors.Is(err, ErrResourceTagProtected) {
		t.Fatalf("favorite edit=%v", err)
	}
}

func TestResourceTagsAssignmentLifecycleAndOwnerFences(t *testing.T) {
	store, _ := resourceTagsFixture(t)
	const owner = "resource-owner"
	tag := resourceTagsCreate(t, store, owner, "Notes").TagID
	foreign := resourceTagsCreate(t, store, "foreign-owner", "Foreign").TagID
	seedLiteratureCollectionItem(t, store, owner, "paper", "Paper")
	seedLiteratureCollectionItem(t, store, "foreign-owner", "foreign-paper", "Foreign")
	seedLiteratureCollectionItem(t, store, owner, "trash", "Trash")
	if _, err := store.db.Exec(`UPDATE literature_items SET deleted_at='2026-01-01T01:00:00Z' WHERE id='trash'`); err != nil {
		t.Fatal(err)
	}
	var metadataRevision int64
	if err := store.db.QueryRow(`SELECT metadata_revision FROM literature_items WHERE id='paper'`).Scan(&metadataRevision); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		tag, id  string
		assigned bool
	}{{tag, "foreign-paper", true}, {foreign, "paper", true}, {tag, "missing", true}, {tag, "trash", true}} {
		if _, err := store.SetResourceTagAssignment(t.Context(), owner, ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: target.tag, ResourceType: "literature.item", ResourceID: target.id, Assigned: target.assigned}); !errors.Is(err, ErrResourceTagUnavailable) {
			t.Fatalf("foreign/missing/trash target=%+v err=%v", target, err)
		}
	}
	input := ResourceTagSetAssignment{OperationID: "assign-paper", TagID: tag, ResourceType: "literature.item", ResourceID: "\uFEFF paper \u00a0", Assigned: true}
	assigned, err := store.SetResourceTagAssignment(t.Context(), owner, input)
	if err != nil || assigned.Snapshot.Revision != 2 || len(assigned.Snapshot.Assignments) != 1 || assigned.Snapshot.Assignments[0].ResourceID != "paper" {
		t.Fatalf("assign=%+v err=%v", assigned, err)
	}
	changed := input
	changed.ResourceID = "paper"
	if _, err := store.SetResourceTagAssignment(t.Context(), owner, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("raw operation identity was normalized: %v", err)
	}
	changed.OperationID = "already-assigned"
	if got, err := store.SetResourceTagAssignment(t.Context(), owner, changed); err != nil || got.Snapshot.Revision != 2 {
		t.Fatalf("desired true no-op=%+v err=%v", got, err)
	}
	if _, err := store.db.Exec(`UPDATE literature_items SET deleted_at='2026-01-01T02:00:00Z' WHERE id='paper'`); err != nil {
		t.Fatal(err)
	}
	if got := resourceTagsRead(t, store, owner); len(got.Assignments) != 1 || got.Revision != 2 {
		t.Fatalf("Trash pruned assignment: %+v", got)
	}
	changed.OperationID, changed.Assigned = "unassign-trash", false
	if got, err := store.SetResourceTagAssignment(t.Context(), owner, changed); err != nil || len(got.Snapshot.Assignments) != 0 || got.Snapshot.Revision != 3 {
		t.Fatalf("unassign Trash=%+v err=%v", got, err)
	}
	changed.OperationID, changed.ResourceID = "unassign-missing", "missing"
	if got, err := store.SetResourceTagAssignment(t.Context(), owner, changed); err != nil || got.Snapshot.Revision != 3 {
		t.Fatalf("unassign missing=%+v err=%v", got, err)
	}
	if _, err := store.db.Exec(`UPDATE literature_items SET deleted_at=NULL WHERE id='paper'`); err != nil {
		t.Fatal(err)
	}
	changed.OperationID, changed.ResourceID, changed.Assigned = "reassign", "paper", true
	if _, err := store.SetResourceTagAssignment(t.Context(), owner, changed); err != nil {
		t.Fatal(err)
	}
	var afterRevision int64
	if err := store.db.QueryRow(`SELECT metadata_revision FROM literature_items WHERE id='paper'`).Scan(&afterRevision); err != nil || afterRevision != metadataRevision {
		t.Fatalf("tag assignment edited bibliography: before=%d after=%d err=%v", metadataRevision, afterRevision, err)
	}
	if _, err := store.db.Exec(`DELETE FROM literature_items WHERE id='paper'`); err != nil {
		t.Fatal(err)
	}
	if got := resourceTagsRead(t, store, owner); len(got.Assignments) != 0 || got.Revision != 5 || resourceTagsCount(t, store, "resource_tag_assignments") != 0 {
		t.Fatalf("canonical hard delete=%+v", got)
	}
	if replay, err := store.SetResourceTagAssignment(t.Context(), owner, input); err != nil || !reflect.DeepEqual(replay, assigned) || len(resourceTagsRead(t, store, owner).Assignments) != 0 {
		t.Fatalf("old assignment receipt reapplied side effect: %+v err=%v", replay, err)
	}
}

func TestResourceTagsUnassignDoesNotRequireCurrentResourceOwnership(t *testing.T) {
	store, _ := resourceTagsFixture(t)
	const owner = "original-owner"
	tag := resourceTagsCreate(t, store, owner, "Original").TagID
	otherTag := resourceTagsCreate(t, store, "new-owner", "New").TagID
	seedLiteratureCollectionItem(t, store, "new-owner", "foreign-paper", "Foreign")
	seedLiteratureCollectionItem(t, store, owner, "transferred-paper", "Transferred")
	var expected ResourceTagSnapshot
	for index, id := range []string{"foreign-paper", "nonexistent-paper"} {
		got, err := store.SetResourceTagAssignment(t.Context(), owner, ResourceTagSetAssignment{
			OperationID: uuid.NewString(), TagID: tag, ResourceType: "literature.item", ResourceID: id, Assigned: false,
		})
		if err != nil || got.Snapshot.Revision != 1 || len(got.Snapshot.Assignments) != 0 {
			t.Fatalf("unassign must not reveal target existence/ownership: id=%q receipt=%+v err=%v", id, got, err)
		}
		if index == 0 {
			expected = got.Snapshot
		} else if !reflect.DeepEqual(got.Snapshot, expected) {
			t.Fatal("foreign and nonexistent unassignment observations differ")
		}
	}
	input := ResourceTagSetAssignment{OperationID: "before-transfer", TagID: tag, ResourceType: "literature.item", ResourceID: "transferred-paper", Assigned: true}
	if _, err := store.SetResourceTagAssignment(t.Context(), owner, input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE literature_items SET owner_user_id='new-owner' WHERE id='transferred-paper'`); err != nil {
		t.Fatal(err)
	}
	input.OperationID, input.TagID = "new-owner-assignment", otherTag
	if _, err := store.SetResourceTagAssignment(t.Context(), "new-owner", input); err != nil {
		t.Fatal(err)
	}
	input.OperationID, input.TagID, input.Assigned = "retire-old-relationship", tag, false
	got, err := store.SetResourceTagAssignment(t.Context(), owner, input)
	if err != nil || got.Snapshot.Revision != 3 {
		t.Fatalf("former owner could not clear own relationship: %+v err=%v", got, err)
	}
	var oldCount, newCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM resource_tag_assignments WHERE owner_user_id=? AND resource_id='transferred-paper'`, owner).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM resource_tag_assignments WHERE owner_user_id='new-owner' AND resource_id='transferred-paper'`).Scan(&newCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || newCount != 1 {
		t.Fatalf("cleanup crossed owner boundary: old=%d new=%d", oldCount, newCount)
	}
}

func TestResourceTagsTextTimestampAndResourceIDBoundaries(t *testing.T) {
	store, _ := resourceTagsFixture(t)
	const owner = "boundary-owner"
	for _, name := range []string{strings.Repeat("x", 65), strings.Repeat(" ", 64) + "x", strings.Repeat("😀", 33), strings.Repeat("İ", 64), string([]byte{0xff})} {
		if _, err := store.CreateResourceTag(t.Context(), owner, ResourceTagCreate{OperationID: uuid.NewString(), Name: name, IconKey: "tag", ColorKey: "blue"}); !errors.Is(err, ErrResourceTagInvalid) {
			t.Fatalf("invalid name accepted: %q err=%v", name, err)
		}
	}
	store.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	created := resourceTagsCreate(t, store, owner, strings.Repeat("😀", 32))
	previous := created.Snapshot.Tags[1]
	store.now = func() time.Time { return time.UnixMilli(previous.UpdatedAt - 100) }
	input := ResourceTagUpdate{OperationID: "monotonic-update", ExpectedUpdatedAt: previous.UpdatedAt, Name: "Updated", IconKey: "bookmark", ColorKey: "red"}
	updated, err := store.UpdateResourceTag(t.Context(), owner, created.TagID, input)
	if err != nil || updated.Snapshot.Tags[1].UpdatedAt != previous.UpdatedAt+1 || updated.Snapshot.Tags[1].ColorKey != "red" {
		t.Fatalf("monotonic update=%+v err=%v", updated, err)
	}
	stale := input
	stale.OperationID = "stale-update"
	if _, err := store.UpdateResourceTag(t.Context(), owner, created.TagID, stale); !errors.Is(err, ErrResourceTagConflict) {
		t.Fatalf("stale CAS=%v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER suppress_tag_update BEFORE UPDATE ON resource_tags BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	stale.ExpectedUpdatedAt = updated.Snapshot.Tags[1].UpdatedAt
	stale.OperationID = "zero-affected-update"
	if _, err := store.UpdateResourceTag(t.Context(), owner, created.TagID, stale); !errors.Is(err, ErrResourceTagConflict) {
		t.Fatalf("zero-row CAS accepted: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER suppress_tag_update`); err != nil {
		t.Fatal(err)
	}
	if got := resourceTagsRead(t, store, owner); got.Revision != 2 {
		t.Fatalf("failed CAS mutated revision: %+v", got)
	}
	if _, err := store.CreateResourceTag(t.Context(), owner, ResourceTagCreate{OperationID: "same-normalized-name", Name: "ＵＰＤＡＴＥＤ", IconKey: "tag", ColorKey: "blue"}); !errors.Is(err, ErrResourceTagNameConflict) {
		t.Fatalf("normalized name collision=%v", err)
	}
	for _, resourceID := range []string{"", "\uFEFF \t", strings.Repeat("😀", 129), string([]byte{0xff})} {
		if _, err := store.SetResourceTagAssignment(t.Context(), owner, ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: created.TagID, ResourceType: "literature.item", ResourceID: resourceID, Assigned: false}); !errors.Is(err, ErrResourceTagInvalid) {
			t.Fatalf("invalid resource ID accepted: %q err=%v", resourceID, err)
		}
	}
	for _, resourceID := range []string{strings.Repeat("😀", 128), "\u0085paper", "paper  with gap"} {
		seedLiteratureCollectionItem(t, store, owner, resourceID, "Boundary reference")
		got, err := store.SetResourceTagAssignment(t.Context(), owner, ResourceTagSetAssignment{OperationID: uuid.NewString(), TagID: created.TagID, ResourceType: "literature.item", ResourceID: "\uFEFF" + resourceID + "\uFEFF", Assigned: true})
		found := false
		for _, assignment := range got.Snapshot.Assignments {
			found = found || assignment.ResourceID == resourceID
		}
		if err != nil || !found {
			t.Fatalf("ECMA trim changed accepted ID=%q result=%+v err=%v", resourceID, got, err)
		}
	}
	for _, at := range []int64{0, ResourceTagTimestampMax + 1} {
		store.now = func() time.Time { return time.UnixMilli(at) }
		if _, err := store.CreateResourceTag(t.Context(), owner, ResourceTagCreate{OperationID: uuid.NewString(), Name: "Clock", IconKey: "tag", ColorKey: "blue"}); !errors.Is(err, ErrResourceTagInvalid) {
			t.Fatalf("invalid timestamp %d accepted: %v", at, err)
		}
	}
}
