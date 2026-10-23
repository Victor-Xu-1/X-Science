package workspace

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func literatureMetadataPrecisionValues() []struct{ name, number string } {
	return []struct{ name, number string }{
		{name: "integer beyond float64 precision", number: "9007199254740993"},
		{name: "high precision decimal", number: "0.12345678901234567890123456789"},
		{name: "valid exponent beyond float64 range", number: "1e400"},
	}
}

func literatureMetadataPrecisionObject(number, label string) map[string]any {
	return map[string]any{
		"label": label,
		"value": json.Number(number),
		"nested": map[string]any{
			"number": json.Number(number),
			"array":  []any{json.Number(number), map[string]any{"number": json.Number(number)}, []any{json.Number(number)}},
		},
	}
}

func literatureMetadataPrecisionJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("cannot marshal precision evidence: %v", err)
	}
	return raw
}

func assertLiteratureMetadataPrecision(t *testing.T, checkpoint string, metadata map[string]any, expected []byte) {
	t.Helper()
	actual := literatureMetadataPrecisionJSON(t, metadata)
	if !bytes.Equal(actual, expected) {
		t.Errorf("%s metadata JSON changed: got %s, want %s", checkpoint, actual, expected)
	}
}

func assertLiteraturePrecisionReceipt(t *testing.T, checkpoint string, actual LiteratureReceipt, expected []byte) {
	t.Helper()
	if wire := literatureMetadataPrecisionJSON(t, actual); !bytes.Equal(wire, expected) {
		t.Errorf("%s original receipt changed: got %s, want %s", checkpoint, wire, expected)
	}
}

func assertLiteraturePrecisionCatalog(t *testing.T, store *Store, owner, id, title string, metadata []byte) {
	t.Helper()
	item, err := store.GetLiteratureItem(t.Context(), owner, id)
	if err != nil {
		t.Errorf("canonical item read failed for valid JSON metadata: %v", err)
	} else {
		if item.ID != id || item.Title != title {
			t.Errorf("canonical item identity changed: %#v", item)
		}
		assertLiteratureMetadataPrecision(t, "canonical item", item.Metadata, metadata)
	}
	page, err := store.ListLiteratureItems(t.Context(), owner, title, "", "", 20)
	if err != nil {
		t.Errorf("catalog listing failed for valid JSON metadata: %v", err)
	} else if page.Schema != "x-science.literature-page.v1" || len(page.Items) != 1 || page.Items[0].ID != id {
		t.Errorf("catalog did not read the exact owned item: %#v", page)
	} else {
		assertLiteratureMetadataPrecision(t, "catalog listing", page.Items[0].Metadata, metadata)
	}
}

func TestLiteratureMetadataPrecisionThroughCatalogAndOriginalReceipts(t *testing.T) {
	for _, test := range literatureMetadataPrecisionValues() {
		t.Run(test.name, func(t *testing.T) {
			const owner = "precision-owner"
			path := filepath.Join(t.TempDir(), "workspace.sqlite")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if store != nil {
					_ = store.Close()
				}
			}()
			if _, err = store.CreateProject(CreateProjectInput{ID: "precision-source", UserID: owner, Name: "Synthetic precision source"}); err != nil {
				t.Fatal(err)
			}
			// As in the existing Library fixture, these are local synthetic PDF
			// bytes with an explicit page count, not a PDF/scientific calculation.
			artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
				ArtifactID: "precision-paper", ProjectID: "precision-source", Name: "synthetic.pdf", Kind: "application/pdf",
				Content: []byte("%PDF-1.4\nsynthetic metadata precision fixture\n%%EOF"),
			})
			if err != nil {
				t.Fatal(err)
			}
			input := LiteratureImport{
				OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID,
				Title: "Original synthetic metadata", Metadata: literatureMetadataPrecisionObject(test.number, "imported"), PageCount: 2,
			}
			importedMetadata := literatureMetadataPrecisionJSON(t, input.Metadata)
			imported, err := store.ImportLiteratureArtifact(t.Context(), owner, input)
			if err != nil {
				t.Fatalf("import rejected valid JSON metadata: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "first import ACK", imported.Item.Metadata, importedMetadata)
			originalImportACK := literatureMetadataPrecisionJSON(t, imported)
			importReplay, err := store.ImportLiteratureArtifact(t.Context(), owner, input)
			if err != nil {
				t.Fatalf("original import replay failed: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "original import replay", importReplay.Item.Metadata, importedMetadata)
			assertLiteraturePrecisionReceipt(t, "immediate import replay", importReplay, originalImportACK)
			assertLiteraturePrecisionCatalog(t, store, owner, imported.Item.ID, input.Title, importedMetadata)

			patchTitle := "Patched synthetic metadata"
			patch := LiteraturePatch{
				OperationID: uuid.NewString(), ExpectedRevision: imported.Item.MetadataRevision,
				Title: &patchTitle, Metadata: literatureMetadataPrecisionObject(test.number, "patched"),
			}
			patchedMetadata := literatureMetadataPrecisionJSON(t, patch.Metadata)
			patched, err := store.PatchLiteratureItem(t.Context(), owner, imported.Item.ID, patch)
			if err != nil {
				t.Fatalf("Patch rejected valid JSON metadata: %v", err)
			}
			if patched.Item.MetadataRevision != imported.Item.MetadataRevision+1 {
				t.Fatalf("Patch did not commit its revision: %#v", patched)
			}
			assertLiteratureMetadataPrecision(t, "first Patch ACK", patched.Item.Metadata, patchedMetadata)
			originalPatchACK := literatureMetadataPrecisionJSON(t, patched)
			patchReplay, err := store.PatchLiteratureItem(t.Context(), owner, imported.Item.ID, patch)
			if err != nil {
				t.Fatalf("original Patch replay failed: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "original Patch replay", patchReplay.Item.Metadata, patchedMetadata)
			assertLiteraturePrecisionReceipt(t, "immediate Patch replay", patchReplay, originalPatchACK)
			assertLiteraturePrecisionCatalog(t, store, owner, imported.Item.ID, patchTitle, patchedMetadata)

			laterTitle := "Later independently committed metadata"
			laterMetadata := literatureMetadataPrecisionObject("42", "later edit")
			later, err := store.PatchLiteratureItem(t.Context(), owner, imported.Item.ID, LiteraturePatch{
				OperationID: uuid.NewString(), ExpectedRevision: patched.Item.MetadataRevision,
				Title: &laterTitle, Metadata: laterMetadata,
			})
			if err != nil || later.Item.MetadataRevision != patched.Item.MetadataRevision+1 {
				t.Fatalf("later metadata mutation did not commit: %#v err=%v", later, err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			// Both original requests now carry stale live revisions/content, but
			// their result-ledger ACKs must remain exact original snapshots.
			importReplay, err = store.ImportLiteratureArtifact(t.Context(), owner, input)
			if err != nil {
				t.Fatalf("reopened original import replay failed: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "reopened import replay", importReplay.Item.Metadata, importedMetadata)
			assertLiteraturePrecisionReceipt(t, "reopened import replay", importReplay, originalImportACK)
			patchReplay, err = store.PatchLiteratureItem(t.Context(), owner, imported.Item.ID, patch)
			if err != nil {
				t.Fatalf("reopened original Patch replay failed: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "reopened Patch replay", patchReplay.Item.Metadata, patchedMetadata)
			assertLiteraturePrecisionReceipt(t, "reopened Patch replay", patchReplay, originalPatchACK)
			assertLiteraturePrecisionCatalog(t, store, owner, imported.Item.ID, laterTitle, literatureMetadataPrecisionJSON(t, laterMetadata))
		})
	}
}

func TestLiteratureMetadataPrecisionTypedItemAndReceipt(t *testing.T) {
	for _, test := range literatureMetadataPrecisionValues() {
		t.Run(test.name, func(t *testing.T) {
			metadata := literatureMetadataPrecisionObject(test.number, "typed wire")
			expected := literatureMetadataPrecisionJSON(t, metadata)
			item := LiteratureItem{ID: "typed-synthetic-item", Title: "Synthetic typed metadata", MetadataRevision: 1,
				Metadata: metadata, ProjectIDs: []string{}, Attachments: []LiteratureAttachment{}}
			var decoded LiteratureItem
			if err := json.Unmarshal(literatureMetadataPrecisionJSON(t, item), &decoded); err != nil {
				t.Fatalf("typed item rejected valid JSON metadata: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "typed item", decoded.Metadata, expected)
			var receipt LiteratureReceipt
			if err := json.Unmarshal(literatureMetadataPrecisionJSON(t, LiteratureReceipt{
				Schema: "x-science.literature-receipt.v1", OperationID: uuid.NewString(), Item: item,
			}), &receipt); err != nil {
				t.Fatalf("typed receipt rejected valid JSON metadata: %v", err)
			}
			assertLiteratureMetadataPrecision(t, "typed receipt item", receipt.Item.Metadata, expected)
		})
	}
}
