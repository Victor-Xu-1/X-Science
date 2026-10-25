package workspace

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMemoryCategoryCreateReplaysOriginalKeyAfterReopen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workspace.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	const owner = "synthetic-category-owner"
	const operationID = "synthetic-category-save-001"
	first, err := store.CreateMemoryCategory(
		WithMutationIdempotencyKey(context.Background(), operationID),
		owner, "Synthetic category", "Synthetic routing guidance", true,
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The caller lost the first response. A new Store/context must consult
	// the canonical receipt, without a fake frame or in-memory result cache.
	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayed, err := reopened.CreateMemoryCategory(
		WithMutationIdempotencyKey(context.Background(), operationID),
		owner, "Synthetic category", "Synthetic routing guidance", true,
	)
	if err != nil {
		t.Fatalf("same owner/key/payload must read the committed receipt: %v", err)
	}
	if first.ID == "" || replayed.ID != first.ID {
		t.Fatalf("receipt replay changed category identity: first=%q replay=%q", first.ID, replayed.ID)
	}
	if !replayed.CreatedAt.Equal(first.CreatedAt) || !replayed.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatal("replay performed a second write instead of reading the original result")
	}
	categories, err := reopened.ListMemoryCategories(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0].ID != first.ID {
		t.Fatalf("expected the single original category; got %#v", categories)
	}
}
