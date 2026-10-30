package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// This isolated fixture retains rollback-writer coverage for an existing row.
// First large writes use the public Store APIs in the capacity write tests.
func seedFutureLiteratureMetadata(t *testing.T, store *Store, item LiteratureItem) map[string]any {
	t.Helper()
	metadata := make(map[string]any, len(item.Metadata)+1)
	for key, value := range item.Metadata {
		metadata[key] = value
	}
	metadata["personal_note"] = strings.Repeat("汉", 6000)
	raw := literatureMetadataPrecisionJSON(t, metadata)
	if len(raw) <= 16384 {
		t.Fatal("future row must exceed the historical complete metadata budget")
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE literature_items SET metadata_json=? WHERE id=? AND owner_user_id=?`, string(raw), item.ID, metadataFieldsOwner); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func assertCapacityMutationUnchanged(t *testing.T, store *Store, item LiteratureItem, metadata map[string]any, ledgerBefore int) {
	t.Helper()
	actual, err := store.GetLiteratureItem(t.Context(), metadataFieldsOwner, item.ID)
	var ledgerAfter int
	ledgerErr := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger`).Scan(&ledgerAfter)
	if err != nil || ledgerErr != nil || ledgerAfter != ledgerBefore || actual.MetadataRevision != item.MetadataRevision || actual.Title != item.Title ||
		!bytes.Equal(literatureMetadataPrecisionJSON(t, actual.Metadata), literatureMetadataPrecisionJSON(t, metadata)) {
		t.Fatalf("rejected capacity mutation changed canonical row or ledger: revision=%d ledger=%d->%d err=%v/%v", actual.MetadataRevision, ledgerBefore, ledgerAfter, err, ledgerErr)
	}
}

func TestLiteratureMetadataCapacityCompatibleWriter(t *testing.T) {
	store, path, seed := newMetadataFieldsFixture(t)
	metadata := seedFutureLiteratureMetadata(t, store, seed.Item)
	assertLiteraturePrecisionCatalog(t, store, metadataFieldsOwner, seed.Item.ID, seed.Item.Title, literatureMetadataPrecisionJSON(t, metadata))

	var ledgerBefore int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger`).Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision,
		MetadataPatch: json.RawMessage(`{"set":{"personal_note":"updated note","year":2025}}`)}
	// Preserve the larger row during the first edit instead of shrinking it to
	// a small object which the historical writer already handles.
	note := strings.Repeat("界", 7000)
	input.MetadataPatch = literatureMetadataPrecisionJSON(t, map[string]any{"set": map[string]any{"personal_note": note, "year": 2025}})
	stale := input
	stale.ExpectedRevision++
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, stale); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("large canonical row bypassed CAS: %v", err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), "foreign-owner", seed.Item.ID, input); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("large canonical row bypassed its owner: %v", err)
	}
	assertCapacityMutationUnchanged(t, store, seed.Item, metadata, ledgerBefore)

	first, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, input)
	if err != nil || first.Item.MetadataRevision != seed.Item.MetadataRevision+1 {
		t.Fatalf("compatibility writer cannot edit a valid future large row: revision=%d err=%v", first.Item.MetadataRevision, err)
	}
	metadata["personal_note"], metadata["year"] = note, 2025
	assertLiteratureMetadataPrecision(t, "large field edit ACK", first.Item.Metadata, literatureMetadataPrecisionJSON(t, metadata))

	title := "Large row remains editable"
	titled, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: first.Item.MetadataRevision, Title: &title,
	})
	if err != nil || titled.Item.MetadataRevision != first.Item.MetadataRevision+1 {
		t.Fatalf("compatibility writer cannot change title on a future large row: %v", err)
	}
	metadata["flag"] = true
	replacement := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: titled.Item.MetadataRevision, Metadata: metadata}
	replaced, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, replacement)
	if err != nil || replaced.Item.MetadataRevision != titled.Item.MetadataRevision+1 {
		t.Fatalf("compatibility writer cannot replace valid large metadata: %v", err)
	}
	ack := literatureMetadataPrecisionJSON(t, replaced)
	hash, ledgerACK := metadataFieldsLedger(t, store, replacement.OperationID)
	removed, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: replaced.Item.MetadataRevision,
		MetadataPatch: json.RawMessage(`{"remove":["personal_note"]}`),
	})
	if err != nil || removed.Item.MetadataRevision != replaced.Item.MetadataRevision+1 {
		t.Fatalf("compatibility writer cannot remove a future note: %v", err)
	}
	remaining := make(map[string]any, len(metadata)-1)
	for key, value := range metadata {
		if key != "personal_note" {
			remaining[key] = value
		}
	}
	assertLiteraturePrecisionCatalog(t, store, metadataFieldsOwner, seed.Item.ID, title, literatureMetadataPrecisionJSON(t, remaining))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// This ACK was committed by the real Patch/CAS/ledger owner above. Its
	// original larger request must replay even after the row becomes small.
	replay, err := reopened.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, replacement)
	if err != nil {
		t.Fatalf("large original request was rejected before immutable replay: %v", err)
	}
	assertLiteraturePrecisionReceipt(t, "reopened large full replacement", replay, ack)
	if afterHash, afterACK := metadataFieldsLedger(t, reopened, replacement.OperationID); afterHash != hash || afterACK != ledgerACK {
		t.Fatal("large replay rewrote its original request identity or ACK")
	}
	assertLiteraturePrecisionCatalog(t, reopened, metadataFieldsOwner, seed.Item.ID, title, literatureMetadataPrecisionJSON(t, remaining))
}

func TestLiteratureMetadataCapacityFutureShapeAndLegacyDomain(t *testing.T) {
	ordinary := strings.Repeat("a", literatureMetadataLegacyBytes-len(`{"abstract":""}`))
	for _, note := range []string{
		strings.Repeat("汉", 6000), strings.Repeat("🌱", 5000),
		strings.Repeat("\x01", 10000), strings.Repeat("<>&\u2028\u2029", 2000),
	} {
		metadata := map[string]any{"abstract": ordinary, "personal_note": note}
		raw, err := normalizeLiteratureMetadata(metadata)
		if err != nil || len(raw) <= literatureMetadataLegacyBytes || !validExpandedLiteratureMetadata(raw) {
			t.Fatalf("bounded UTF16 note with full ordinary budget is not a valid future shape: bytes=%d err=%v", len(raw), err)
		}
		if _, err := literatureMetadata(metadata); !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("future catalog shape expanded discovery metadata admission: %v", err)
		}
	}
	for _, opaque := range []any{nil, true, map[string]any{"opaque": json.Number("1e400")}, strings.Repeat("x", 11000)} {
		metadata := map[string]any{"personal_note": opaque}
		raw, err := literatureMetadata(metadata)
		if err != nil || !bytes.Equal([]byte(raw), literatureMetadataPrecisionJSON(t, metadata)) || admitNewLiteratureCatalogMetadata(raw) != nil {
			t.Fatalf("compatibility narrowed the old <=16KiB opaque note domain: %v", err)
		}
	}
}

func TestLiteratureMetadataCapacityRejectsInvalidExpandedUpdatesAtomically(t *testing.T) {
	store, _, seed := newMetadataFieldsFixture(t)
	metadata := seedFutureLiteratureMetadata(t, store, seed.Item)
	var ledgerBefore int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger`).Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []map[string]any{
		{"personal_note": strings.Repeat("汉", 10001)},
		{"personal_note": strings.Repeat("🌱", 5001)},
		{"personal_note": "valid note", "abstract": strings.Repeat("a", 16384)},
		{"personal_note": map[string]any{"opaque": strings.Repeat("a", 20000)}},
		{"personal_note": strings.Repeat("<", 14000)},
	} {
		_, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, LiteraturePatch{
			OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision, Metadata: replacement,
		})
		if !errors.Is(err, ErrLiteratureInvalid) {
			t.Fatalf("invalid expanded replacement was admitted: %v", err)
		}
		assertCapacityMutationUnchanged(t, store, seed.Item, metadata, ledgerBefore)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_capacity_ledger BEFORE INSERT ON mutation_result_ledger BEGIN SELECT RAISE(ABORT,'injected capacity ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seed.Item.ID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: seed.Item.MetadataRevision,
		MetadataPatch: json.RawMessage(`{"set":{"year":2025}}`),
	})
	if err == nil || !strings.Contains(err.Error(), "injected capacity ledger failure") {
		t.Fatalf("late future-row ledger failure was hidden: %v", err)
	}
	assertCapacityMutationUnchanged(t, store, seed.Item, metadata, ledgerBefore)
}
