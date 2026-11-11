package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"reflect"
	"testing"
)

func TestLiteratureRestorePreviewSnapshotPrivacyOrderAndScope(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	ctx := t.Context()
	const owner, project = "restore-preview-owner", "restore-preview-project"
	if _, err := store.CreateProject(CreateProjectInput{ID: project, UserID: owner, Name: "Restore preview"}); err != nil {
		t.Fatal(err)
	}
	collection := mustCreateLiteratureCollection(t, store, owner, "Restore subset", "")
	for _, id := range []string{"z-restore", "a-restore", "active-reference", "outside-project"} {
		seedLiteratureCollectionItem(t, store, owner, id, "Matching reference")
		if _, err := store.SetLiteratureCollectionItem(ctx, owner, collection.CollectionID, id, uuid.NewString(), true); err != nil {
			t.Fatal(err)
		}
		if id != "outside-project" {
			if _, err := store.SetLiteratureProject(ctx, owner, id, project, uuid.NewString(), true); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedLiteratureCollectionItem(t, store, "foreign-owner", "foreign-reference", "Matching reference")
	deleted := true
	expected := []LiteratureRestoreTarget{}
	for _, id := range []string{"z-restore", "a-restore", "outside-project"} {
		item, err := store.GetLiteratureItem(ctx, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := store.PatchLiteratureItem(ctx, owner, id, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: item.MetadataRevision, Deleted: &deleted})
		if err != nil {
			t.Fatal(err)
		}
		if id != "outside-project" {
			expected = append(expected, LiteratureRestoreTarget{ID: id, MetadataRevision: receipt.Item.MetadataRevision})
		}
	}
	filters := LiteratureQueryFilters{Query: "Matching", ProjectID: project, CollectionID: collection.CollectionID, Lifecycle: LiteratureLifecycleDeleted}
	preview, err := store.PreviewLiteratureRestore(ctx, owner, filters, LiteratureSelectionReadOptions{})
	if err != nil || preview.Schema != "x-science.literature-restore-preview.v1" || preview.TotalCount != 2 || !reflect.DeepEqual(preview.Items, expected) {
		t.Fatalf("snapshot lost order/revision/scope: %+v %v", preview, err)
	}
	raw, _ := json.Marshal(preview)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		t.Fatalf("open preview envelope: %s", raw)
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(fields["items"], &items) != nil {
		t.Fatal("invalid item array")
	}
	for _, item := range items {
		if len(item) != 2 || item["id"] == nil || item["metadata_revision"] == nil {
			t.Fatalf("private detail escaped preview: %s", raw)
		}
	}
	legacy, err := store.ListLiteratureItemIDsInLifecycle(ctx, owner, filters)
	if err != nil || !reflect.DeepEqual(legacy.ItemIDs, []string{"z-restore", "a-restore"}) {
		t.Fatalf("legacy membership changed: %+v %v", legacy, err)
	}
	legacyRaw, _ := json.Marshal(legacy)
	fields = nil
	if json.Unmarshal(legacyRaw, &fields) != nil || len(fields) != 3 || fields["item_ids"] == nil || string(fields["schema"]) != `"x-science.literature-selection.v1"` {
		t.Fatalf("legacy v1 shape changed: %s", legacyRaw)
	}
	if _, err := store.PreviewLiteratureRestore(ctx, "foreign-owner", filters, LiteratureSelectionReadOptions{}); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign scope accepted: %v", err)
	}
	filters.Query = "missing"
	empty, err := store.PreviewLiteratureRestore(ctx, owner, filters, LiteratureSelectionReadOptions{})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.TotalCount != 0 {
		t.Fatalf("empty preview not complete: %+v %v", empty, err)
	}
}

func TestLiteratureRestorePreviewRejectsActiveAndCancelledReads(t *testing.T) {
	store, _ := literatureCollectionStoreFixture(t)
	for _, lifecycle := range []LiteratureLifecycle{"", LiteratureLifecycleActive} {
		if _, err := store.PreviewLiteratureRestore(t.Context(), "restore-owner", LiteratureQueryFilters{Lifecycle: lifecycle}, LiteratureSelectionReadOptions{}); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("nondeleted preview accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PreviewLiteratureRestore(ctx, "restore-owner", LiteratureQueryFilters{Lifecycle: LiteratureLifecycleDeleted}, LiteratureSelectionReadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	if _, err := store.PreviewLiteratureRestore(t.Context(), "restore-owner", LiteratureQueryFilters{Lifecycle: LiteratureLifecycleDeleted}, LiteratureSelectionReadOptions{Order: "unexpected"}); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("unknown order accepted: %v", err)
	}
}
