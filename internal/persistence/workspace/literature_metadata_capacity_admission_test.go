package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureMetadataCapacityFirstWriteKeepsCASAndAtomicity(t *testing.T) {
	store, _, seed := newMetadataFieldsFixture(t)
	metadata := capacityCatalogMetadata(t, seed.Item.Metadata, strings.Repeat("汉", 6000))
	input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision,
		MetadataPatch: literatureMetadataPrecisionJSON(t, map[string]any{"set": metadata})}
	before := capacityCatalogCounts(t, store)
	stale := input
	stale.ExpectedRevision++
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, stale); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("first large write bypassed CAS: %v", err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), "foreign-owner", seed.Item.ID, input); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("first large write bypassed its owner: %v", err)
	}
	assertCapacityMutationUnchanged(t, store, seed.Item, seed.Item.Metadata, before[2])
	if _, err := store.db.Exec(`CREATE TRIGGER fail_first_capacity_ledger BEFORE INSERT ON mutation_result_ledger BEGIN SELECT RAISE(ABORT,'injected first capacity failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, input); err == nil || !strings.Contains(err.Error(), "injected first capacity failure") {
		t.Fatalf("first large write hid late ledger failure: %v", err)
	}
	assertCapacityMutationUnchanged(t, store, seed.Item, seed.Item.Metadata, before[2])
	if capacityCatalogCounts(t, store) != before {
		t.Fatal("rejected/failed first write left a catalog or attachment effect")
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_first_capacity_ledger`); err != nil {
		t.Fatal(err)
	}
	first, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, input)
	if err != nil || first.Item.MetadataRevision != seed.Item.MetadataRevision+1 {
		t.Fatalf("rolled-back first write could not commit the same original request: %v", err)
	}
	assertLiteratureMetadataPrecision(t, "first write after rollback", first.Item.Metadata, literatureMetadataPrecisionJSON(t, metadata))
}

func TestLiteratureMetadataCapacityRejectsInvalidFirstWrites(t *testing.T) {
	store, _, seed := newMetadataFieldsFixture(t)
	ordinaryOver := capacityCatalogMetadata(t, seed.Item.Metadata, strings.Repeat("汉", 6000))
	ordinaryOver["abstract"] = ordinaryOver["abstract"].(string) + "a"
	before := capacityCatalogCounts(t, store)
	for _, test := range []struct {
		name     string
		metadata map[string]any
	}{
		{name: "CJK over 10000 units", metadata: capacityCatalogMetadata(t, seed.Item.Metadata, strings.Repeat("汉", 10001))},
		{name: "astral over 10000 units", metadata: capacityCatalogMetadata(t, seed.Item.Metadata, strings.Repeat("🌱", 5001))},
		{name: "ordinary metadata over original budget", metadata: ordinaryOver},
		{name: "expanded opaque note", metadata: map[string]any{"personal_note": map[string]any{"opaque": strings.Repeat("a", 20000)}}},
		{name: "canonical snapshot over receiver bound", metadata: map[string]any{"personal_note": strings.Repeat("<", 14000)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(literatureMetadataPrecisionJSON(t, test.metadata)) <= literatureMetadataLegacyBytes {
				t.Fatal("negative fixture must reach expanded admission without narrowing the legacy domain")
			}
			_, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, LiteraturePatch{
				OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision, Metadata: test.metadata,
			})
			if !errors.Is(err, ErrLiteratureInvalid) {
				t.Fatalf("invalid first expanded write was admitted: %v", err)
			}
			assertCapacityMutationUnchanged(t, store, seed.Item, seed.Item.Metadata, before[2])
		})
	}
}

func TestLiteratureMetadataCapacityLegacyCatalogWritesRemainAccepted(t *testing.T) {
	store := candidateTestStore(t)
	for _, test := range []struct {
		name string
		note any
	}{
		{name: "null", note: nil},
		{name: "boolean", note: true},
		{name: "opaque precise object", note: map[string]any{"value": json.Number("1e400")}},
		{name: "legacy 11000 ASCII units", note: strings.Repeat("x", 11000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := map[string]any{"personal_note": test.note, "precise": json.Number("9007199254740993")}
			want := literatureMetadataPrecisionJSON(t, metadata)
			if len(want) > literatureMetadataLegacyBytes {
				t.Fatal("legacy fixture exceeds its original complete metadata byte budget")
			}
			first, err := store.CreateManualLiterature(t.Context(), metadataFieldsOwner, LiteratureManualCreate{
				OperationID: uuid.NewString(), Title: "Legacy " + test.name, Metadata: metadata,
			})
			if err != nil {
				t.Fatalf("old <=16KiB catalog domain was narrowed: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "legacy catalog ACK", first.Item.Metadata, want)
			assertLiteraturePrecisionCatalog(t, store, metadataFieldsOwner, first.Item.ID, first.Item.Title, want)
		})
	}
}
