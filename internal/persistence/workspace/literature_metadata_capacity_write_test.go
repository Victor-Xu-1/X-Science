package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLiteratureMetadataCapacityFirstLargeCatalogWrite(t *testing.T) {
	store, _, seed := newMetadataFieldsFixture(t)
	note := strings.Repeat("汉", 6000)
	input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision,
		MetadataPatch: literatureMetadataPrecisionJSON(t, map[string]any{"set": map[string]any{"personal_note": note}})}
	first, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, input)
	if err != nil || first.Item.MetadataRevision != seed.Item.MetadataRevision+1 {
		t.Fatalf("first large catalog note did not commit through original CAS owner: revision=%d err=%v", first.Item.MetadataRevision, err)
	}
	expected := make(map[string]any, len(seed.Item.Metadata)+1)
	for key, value := range seed.Item.Metadata {
		expected[key] = value
	}
	expected["personal_note"] = note
	want := literatureMetadataPrecisionJSON(t, expected)
	assertLiteratureMetadataPrecision(t, "first large note ACK", first.Item.Metadata, want)
	assertLiteraturePrecisionCatalog(t, store, metadataFieldsOwner, seed.Item.ID, seed.Item.Title, want)
}

func capacityCatalogMetadata(t *testing.T, prior map[string]any, note string) map[string]any {
	t.Helper()
	metadata := make(map[string]any, len(prior)+2)
	for key, value := range prior {
		metadata[key] = value
	}
	metadata["abstract"] = ""
	metadata["abstract"] = strings.Repeat("a", literatureMetadataLegacyBytes-len(literatureMetadataPrecisionJSON(t, metadata)))
	if len(literatureMetadataPrecisionJSON(t, metadata)) != literatureMetadataLegacyBytes {
		t.Fatal("ordinary metadata must exercise its complete original byte budget")
	}
	metadata["personal_note"] = note
	return metadata
}

type capacityCatalogMutation struct {
	run      func(*Store) (LiteratureReceipt, error)
	conflict func(*Store) error
}

func newCapacityCatalogMutation(t *testing.T, store *Store, seed LiteratureReceipt, kind string, metadata map[string]any) capacityCatalogMutation {
	t.Helper()
	title := "Capacity " + kind
	switch kind {
	case "delta", "full":
		input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision, Title: &title}
		if kind == "delta" {
			input.MetadataPatch = literatureMetadataPrecisionJSON(t, map[string]any{"set": metadata})
		} else {
			input.Metadata = metadata
		}
		return capacityCatalogMutation{
			run: func(db *Store) (LiteratureReceipt, error) {
				return db.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, input)
			},
			conflict: func(db *Store) error {
				changed, title := input, "Changed immutable capacity request"
				changed.Title = &title
				_, err := db.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, changed)
				return err
			},
		}
	case "manual":
		input := LiteratureManualCreate{OperationID: uuid.NewString(), Title: title, Metadata: metadata}
		return capacityCatalogMutation{
			run: func(db *Store) (LiteratureReceipt, error) {
				return db.CreateManualLiterature(t.Context(), metadataFieldsOwner, input)
			},
			conflict: func(db *Store) error {
				changed := input
				changed.Title = "Changed immutable capacity request"
				_, err := db.CreateManualLiterature(t.Context(), metadataFieldsOwner, changed)
				return err
			},
		}
	case "import":
		artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
			ArtifactID: "capacity-source", ProjectID: "metadata-fields-source", Name: "capacity.pdf", Kind: "application/pdf",
			Content: []byte("%PDF-1.4\nsynthetic capacity source\n%%EOF"),
		})
		if err != nil {
			t.Fatal(err)
		}
		input := LiteratureImport{OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID, Title: title, Metadata: metadata, PageCount: 2}
		return capacityCatalogMutation{
			run: func(db *Store) (LiteratureReceipt, error) {
				return db.ImportLiteratureArtifact(t.Context(), metadataFieldsOwner, input)
			},
			conflict: func(db *Store) error {
				changed := input
				changed.Title = "Changed immutable capacity request"
				_, err := db.ImportLiteratureArtifact(t.Context(), metadataFieldsOwner, changed)
				return err
			},
		}
	default:
		t.Fatalf("unsupported capacity fixture: %s", kind)
		return capacityCatalogMutation{}
	}
}

func capacityCatalogCounts(t *testing.T, store *Store) [3]int {
	t.Helper()
	var counts [3]int
	if err := store.db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM literature_items),(SELECT COUNT(*) FROM literature_attachment_versions),(SELECT COUNT(*) FROM mutation_result_ledger)`).Scan(&counts[0], &counts[1], &counts[2]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestLiteratureMetadataCapacityExpandedCatalogTransactions(t *testing.T) {
	for _, test := range []struct{ kind, note string }{
		{kind: "delta", note: strings.Repeat("汉", 10000)},
		{kind: "full", note: strings.Repeat("🌱", 5000)},
		{kind: "manual", note: strings.Repeat("\x01", 10000)},
		{kind: "import", note: strings.Repeat("<>&\u2028\u2029", 2000)},
	} {
		t.Run(test.kind, func(t *testing.T) {
			store, path, seed := newMetadataFieldsFixture(t)
			metadata := capacityCatalogMetadata(t, seed.Item.Metadata, test.note)
			mutation := newCapacityCatalogMutation(t, store, seed, test.kind, metadata)
			first, err := mutation.run(store)
			wantRevision := int64(1)
			if test.kind == "delta" || test.kind == "full" {
				wantRevision = seed.Item.MetadataRevision + 1
			}
			if err != nil || first.OperationID == "" || first.Item.MetadataRevision != wantRevision {
				t.Fatalf("first expanded %s transaction failed: %v", test.kind, err)
			}
			want, ack := literatureMetadataPrecisionJSON(t, metadata), literatureMetadataPrecisionJSON(t, first)
			assertLiteratureMetadataPrecision(t, "first expanded ACK", first.Item.Metadata, want)
			assertLiteraturePrecisionCatalog(t, store, metadataFieldsOwner, first.Item.ID, first.Item.Title, want)
			later, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, first.Item.ID, LiteraturePatch{
				OperationID: uuid.NewString(), ExpectedRevision: first.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"remove":["personal_note"]}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			before := capacityCatalogCounts(t, store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			replay, err := mutation.run(reopened)
			if err != nil {
				t.Fatalf("original expanded %s request did not replay after later edit and reopen: %v", test.kind, err)
			}
			assertLiteraturePrecisionReceipt(t, "reopened expanded request", replay, ack)
			if err := mutation.conflict(reopened); !errors.Is(err, ErrMutationIdempotencyConflict) {
				t.Fatalf("changed expanded request borrowed original receipt: %v", err)
			}
			current, err := reopened.GetLiteratureItem(t.Context(), metadataFieldsOwner, first.Item.ID)
			if err != nil || current.MetadataRevision != later.Item.MetadataRevision || capacityCatalogCounts(t, reopened) != before {
				t.Fatalf("expanded replay/conflict repeated an effect: revision=%d err=%v", current.MetadataRevision, err)
			}
			assertLiteraturePrecisionCatalog(t, reopened, metadataFieldsOwner, first.Item.ID, first.Item.Title, literatureMetadataPrecisionJSON(t, later.Item.Metadata))
		})
	}
}
