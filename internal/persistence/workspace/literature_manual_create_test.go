package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManualLiteratureAtomicProjectAndDurableReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if _, err := s.CreateProject(CreateProjectInput{ID: "project", UserID: "owner", Name: "Synthetic project", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	input := LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Manual reference", Metadata: map[string]any{"doi": "10.1234/manual", "precise": json.Number("9007199254740993")}, ProjectID: "project"}
	first, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil || first.Item.ID == "" || first.Item.MetadataRevision != 1 || len(first.Item.Attachments) != 0 || !reflect.DeepEqual(first.Item.ProjectIDs, []string{"project"}) {
		t.Fatalf("atomic metadata create: %#v %v", first, err)
	}
	var candidates, ledger int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM literature_candidates`).Scan(&candidates); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id='owner' AND operation='literature.manual-create'`).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if candidates != 0 || ledger != 1 {
		t.Fatalf("hidden candidate or missing atomic receipt: %d/%d", candidates, ledger)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("durable exact receipt: %#v %v", replay, err)
	}
	input.Title = "Different immutable input"
	if _, err := s.CreateManualLiterature(ctx, "owner", input); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed original request: %v", err)
	}
	input.OperationID = uuid.NewString()
	input.ProjectID = "foreign"
	if _, err := s.CreateManualLiterature(ctx, "owner", input); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("project admission: %v", err)
	}
	page, err := s.ListLiteratureItems(ctx, "owner", "", "", "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("failed project admission wrote item: %#v %v", page, err)
	}
}

func TestManualLiteratureUniqueReuseAndTrashConflict(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	input := LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Original", Metadata: map[string]any{"doi": "10.1234/manual", "authors": []string{"Original author"}}}
	first, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	input.OperationID = uuid.NewString()
	input.Title = "Must not overwrite"
	input.Metadata = map[string]any{"doi": "https://doi.org/10.1234/MANUAL"}
	reused, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil || reused.Item.ID != first.Item.ID || reused.Item.Title != "Original" || !reflect.DeepEqual(reused.Item.Metadata, first.Item.Metadata) {
		t.Fatalf("unique active reuse: %#v %v", reused, err)
	}
	yes := true
	if _, err := s.PatchLiteratureItem(ctx, "owner", first.Item.ID, LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: 1, Deleted: &yes}); err != nil {
		t.Fatal(err)
	}
	input.OperationID = uuid.NewString()
	if _, err := s.CreateManualLiterature(ctx, "owner", input); !errors.Is(err, ErrLiteratureTrashConflict) {
		t.Fatalf("must not silently restore/create around Trash: %v", err)
	}
	other, err := s.CreateManualLiterature(ctx, "other", input)
	if err != nil || other.Item.ID == first.Item.ID {
		t.Fatalf("owner separation: %#v %v", other, err)
	}
	input.OperationID = uuid.NewString()
	input.Metadata = map[string]any{}
	if _, err := s.CreateManualLiterature(ctx, "owner", input); err != nil {
		t.Fatalf("identifier-free creation: %v", err)
	}
}

func TestManualLiteratureAmbiguousIdentifiersNeverWrite(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	for _, meta := range []map[string]any{{"doi": "10.1234/a"}, {"pmid": "12345"}} {
		if _, err := s.CreateManualLiterature(ctx, "owner", LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Seed", Metadata: meta}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateManualLiterature(ctx, "owner", LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Ambiguous", Metadata: map[string]any{"doi": "10.1234/a", "pmid": "12345"}})
	if !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("ambiguous match: %v", err)
	}
	page, err := s.ListLiteratureItems(ctx, "owner", "", "", "", 20)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("conflict wrote item: %#v %v", page, err)
	}
}

func TestManualLiteratureCollectionAdmissionIsAtomicAndRevisionBound(t *testing.T) {
	s := candidateTestStore(t)
	ctx := context.Background()
	collection := mustCreateLiteratureCollection(t, s, "owner", "Manual collection", "")
	input := LiteratureManualCreate{OperationID: uuid.NewString(), Title: "Collected", CollectionID: collection.CollectionID, ExpectedCollectionRevision: collection.Collection.Revision}
	first, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM literature_collection_items WHERE collection_id=? AND item_id=?`, collection.CollectionID, first.Item.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing atomic membership %d %v", count, err)
	}
	_, err = s.UpdateLiteratureCollection(ctx, "owner", collection.CollectionID, LiteratureCollectionUpdate{OperationID: uuid.NewString(), ExpectedRevision: collection.Collection.Revision, Name: "Renamed"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateManualLiterature(ctx, "owner", input)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("original receipt before current revision checks: %#v %v", replay, err)
	}
	input.OperationID = uuid.NewString()
	if _, err := s.CreateManualLiterature(ctx, "owner", input); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("stale collection: %v", err)
	}
	if _, err := s.CreateManualLiterature(ctx, "foreign", input); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("foreign collection: %v", err)
	}
	page, err := s.ListLiteratureItems(ctx, "owner", "", "", "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("refusal created a hidden item: %#v %v", page, err)
	}
}
