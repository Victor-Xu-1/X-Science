package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const metadataFieldsOwner = "metadata-fields-owner"

func newMetadataFieldsFixture(t *testing.T) (*Store, string, LiteratureReceipt) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "metadata-fields-source", UserID: metadataFieldsOwner, Name: "Synthetic metadata"}); err != nil {
		t.Fatal(err)
	}
	artifact, version, err := store.SaveArtifactVersion(SaveArtifactVersionInput{
		ArtifactID: "metadata-fields-paper", ProjectID: "metadata-fields-source", Name: "synthetic.pdf", Kind: "application/pdf",
		Content: []byte("%PDF-1.4\nsynthetic metadata field fixture\n%%EOF"),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := decodeLiteratureMetadataObject(`{"authors":["Ada","Grace"],"year":2024,"opaque":{"integer":9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1e400,"array":[1e400,{"n":9007199254740993}]},"flag":false,"nullable":"replace explicitly"}`)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ImportLiteratureArtifact(t.Context(), metadataFieldsOwner, LiteratureImport{
		OperationID: uuid.NewString(), ArtifactID: artifact.ID, VersionID: version.ID,
		Title: "Original metadata fields", Metadata: metadata, PageCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, path, receipt
}

func metadataFieldsLedger(t *testing.T, store *Store, operation string) (string, string) {
	t.Helper()
	var hash, ack string
	if err := store.db.QueryRowContext(t.Context(), `SELECT request_hash,result_json FROM mutation_result_ledger WHERE owner_user_id=? AND idempotency_key=? AND operation='literature.patch'`,
		metadataFieldsOwner, operation).Scan(&hash, &ack); err != nil {
		t.Fatal(err)
	}
	return hash, ack
}

func TestLiteratureMetadataFieldsCommitAndFrozenReplay(t *testing.T) {
	store, path, seeded := newMetadataFieldsFixture(t)
	input := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seeded.Item.MetadataRevision,
		MetadataPatch: json.RawMessage(`{"set":{"authors":"Edited","nullable":null,"__proto__":{"polluted":true}},"remove":["flag","unused"]}`)}
	originalInput := append([]byte(nil), input.MetadataPatch...)
	first, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, input)
	if err != nil || first.Item.MetadataRevision != seeded.Item.MetadataRevision+1 {
		t.Fatal(first, err)
	}
	expected := make(map[string]any, len(seeded.Item.Metadata)+1)
	for key, value := range seeded.Item.Metadata {
		expected[key] = value
	}
	expected["authors"], expected["nullable"], expected["__proto__"] = "Edited", nil, map[string]any{"polluted": true}
	delete(expected, "flag")
	want := literatureMetadataPrecisionJSON(t, expected)
	if !bytes.Equal(literatureMetadataPrecisionJSON(t, first.Item.Metadata), want) || !bytes.Equal(input.MetadataPatch, originalInput) {
		t.Fatal("field edit changed unedited numbers, null/removal, own key, or caller input")
	}
	var raw string
	if err := store.db.QueryRowContext(t.Context(), `SELECT metadata_json FROM literature_items WHERE id=?`, seeded.Item.ID).Scan(&raw); err != nil || !bytes.Equal([]byte(raw), want) {
		t.Fatalf("canonical numeric tokens changed: %s err=%v", raw, err)
	}
	hash, ack := metadataFieldsLedger(t, store, input.OperationID)
	laterTitle := "Later independent title"
	later, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: first.Item.MetadataRevision, Title: &laterTitle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// Reordering equivalent operators/keys must preserve the frozen command,
	// not merge a second time against this now newer canonical revision.
	input.MetadataPatch = json.RawMessage(`{"remove":["unused","flag"],"set":{"__proto__":{"polluted":true},"nullable":null,"authors":"Edited"}}`)
	replayed, err := reopened.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, input)
	if err != nil || !bytes.Equal(literatureMetadataPrecisionJSON(t, replayed), literatureMetadataPrecisionJSON(t, first)) {
		t.Fatalf("reopened delta did not replay its original outcome: %#v %v", replayed, err)
	}
	changed := input
	changed.MetadataPatch = json.RawMessage(`{"set":{"authors":"Different"}}`)
	if _, err := reopened.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, changed); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("changed delta borrowed original ACK: %v", err)
	}
	whole := LiteraturePatch{OperationID: input.OperationID, ExpectedRevision: input.ExpectedRevision, Metadata: expected}
	if _, err := reopened.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, whole); !errors.Is(err, ErrMutationIdempotencyConflict) {
		t.Fatalf("whole replacement borrowed a delta ACK: %v", err)
	}
	current, err := reopened.GetLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID)
	if err != nil || current.MetadataRevision != later.Item.MetadataRevision || current.Title != laterTitle || !bytes.Equal(literatureMetadataPrecisionJSON(t, current.Metadata), want) {
		t.Fatalf("replay/conflict repeated effects or changed canonical data: %#v %v", current, err)
	}
	if afterHash, afterACK := metadataFieldsLedger(t, reopened, input.OperationID); afterHash != hash || afterACK != ack {
		t.Fatal("replay/conflict rewrote the original request/ACK")
	}
}

func TestLiteratureMetadataFieldsCASAndRollback(t *testing.T) {
	store, _, seeded := newMetadataFieldsFixture(t)
	before := literatureMetadataPrecisionJSON(t, seeded.Item.Metadata)
	var countBefore int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger`).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	valid := func() LiteraturePatch {
		return LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seeded.Item.MetadataRevision,
			MetadataPatch: json.RawMessage(`{"set":{"authors":"Edited"}}`)}
	}
	stale := valid()
	stale.ExpectedRevision++
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, stale); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("CAS omitted: %v", err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), "foreign-owner", seeded.Item.ID, valid()); !errors.Is(err, ErrLiteratureUnavailable) {
		t.Fatalf("owner fence omitted: %v", err)
	}
	both := valid()
	both.Metadata = map[string]any{}
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, both); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("whole/delta collision accepted: %v", err)
	}
	both = valid()
	deleted := false
	both.Deleted = &deleted
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, both); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("deleted/delta collision accepted: %v", err)
	}
	large := valid()
	large.MetadataPatch = literatureMetadataPrecisionJSON(t, map[string]any{"set": map[string]any{"fill": strings.Repeat("x", 16200)}})
	if len(large.MetadataPatch) >= 16384 {
		t.Fatal("fixture must reach the merged-object limit, not the patch limit")
	}
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, large); !errors.Is(err, ErrLiteratureInvalid) {
		t.Fatalf("merged metadata bound omitted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PatchLiteratureItem(ctx, metadataFieldsOwner, seeded.Item.ID, valid()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation swallowed: %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_metadata_field_ledger BEFORE INSERT ON mutation_result_ledger BEGIN SELECT RAISE(ABORT,'injected field ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, valid()); err == nil || !strings.Contains(err.Error(), "injected field ledger failure") {
		t.Fatalf("late ledger error did not roll back: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_metadata_field_ledger`); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID)
	var countAfter int
	countErr := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger`).Scan(&countAfter)
	if err != nil || countErr != nil || countAfter != countBefore || current.MetadataRevision != seeded.Item.MetadataRevision ||
		current.Title != seeded.Item.Title || !bytes.Equal(literatureMetadataPrecisionJSON(t, current.Metadata), before) {
		t.Fatalf("rejected/failed delta left effects: %#v ledger=%d->%d err=%v/%v", current, countBefore, countAfter, err, countErr)
	}
	t.Run("large-actions-small-final-object", func(t *testing.T) {
		store, _, seeded := newMetadataFieldsFixture(t)
		longKey := strings.Repeat("k", 9000)
		original := map[string]any{longKey: "old value", "opaque": json.Number("1e400")}
		before, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, LiteraturePatch{
			OperationID: uuid.NewString(), ExpectedRevision: seeded.Item.MetadataRevision, Metadata: original,
		})
		if err != nil {
			t.Fatal(err)
		}
		replacement := strings.Repeat("v", 10000)
		patch := literatureMetadataPrecisionJSON(t, map[string]any{
			"set": map[string]any{"replacement": replacement}, "remove": []string{longKey},
		})
		expected := literatureMetadataPrecisionJSON(t, map[string]any{"replacement": replacement, "opaque": json.Number("1e400")})
		if len(patch) <= 16384 || len(patch) >= 64<<10 || len(expected) >= 16384 {
			t.Fatal("fixture must exceed only the extra action limit, within the original envelope and final-object bound")
		}
		result, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, LiteraturePatch{
			OperationID: uuid.NewString(), ExpectedRevision: before.Item.MetadataRevision, MetadataPatch: patch,
		})
		if err != nil || result.Item.MetadataRevision != before.Item.MetadataRevision+1 ||
			!bytes.Equal(literatureMetadataPrecisionJSON(t, result.Item.Metadata), expected) {
			t.Fatalf("valid field delta was rejected by an extra action-size gate: revision=%d err=%v", result.Item.MetadataRevision, err)
		}
		var raw string
		if err := store.db.QueryRowContext(t.Context(), `SELECT metadata_json FROM literature_items WHERE id=?`, seeded.Item.ID).Scan(&raw); err != nil || !bytes.Equal([]byte(raw), expected) {
			t.Fatalf("large action did not retain its precise small final metadata: err=%v", err)
		}
	})
}

func TestLiteratureMetadataFieldsExactIdentityCannotBorrowOldACK(t *testing.T) {
	for _, encoding := range []string{"v2", "legacy"} {
		t.Run(encoding, func(t *testing.T) {
			store, _, seeded := newMetadataFieldsFixture(t)
			title := "Original title-only operation"
			old := LiteraturePatch{OperationID: uuid.NewString(), ExpectedRevision: seeded.Item.MetadataRevision, Title: &title}
			first, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, old)
			if err != nil {
				t.Fatal(err)
			}
			oldIdentity, err := literaturePatchRequestIdentity(seeded.Item.ID, old, "{}")
			if err != nil {
				t.Fatal(err)
			}
			if encoding == "legacy" {
				// Only this isolated genuine receipt's hash is placed into its old
				// format; the successful ACK/business state is never synthesized.
				if _, err := store.db.ExecContext(t.Context(), `UPDATE mutation_result_ledger SET request_hash=? WHERE owner_user_id=? AND idempotency_key=? AND operation='literature.patch'`,
					oldIdentity.legacyExact, metadataFieldsOwner, old.OperationID); err != nil {
					t.Fatal(err)
				}
			}
			hash, ack := metadataFieldsLedger(t, store, old.OperationID)
			if replay, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, old); err != nil ||
				!bytes.Equal(literatureMetadataPrecisionJSON(t, replay), literatureMetadataPrecisionJSON(t, first)) {
				t.Fatal(replay, err)
			}
			delta := old
			delta.MetadataPatch = json.RawMessage(`{"set":{"year":2024}}`)
			if _, err := store.PatchLiteratureItem(t.Context(), metadataFieldsOwner, seeded.Item.ID, delta); !errors.Is(err, ErrMutationIdempotencyConflict) {
				t.Fatalf("delta borrowed old %s ACK or reached CAS instead: %v", encoding, err)
			}
			identity, err := literaturePatchRequestIdentity(seeded.Item.ID, delta, "{}")
			if err != nil || identity.matches(identity.exact) || !identity.matches(identity.requestHash()) {
				t.Fatalf("new delta did not remain exact-only: %v", err)
			}
			if afterHash, afterACK := metadataFieldsLedger(t, store, old.OperationID); afterHash != hash || afterACK != ack {
				t.Fatal("delta rewrote old original receipt")
			}
		})
	}
	// Pin the old typed layout independently of the new optional struct field.
	for _, present := range []bool{false, true} {
		old := struct {
			OperationID      string         `json:"operation_id"`
			ExpectedRevision int64          `json:"expected_revision"`
			Title            *string        `json:"title,omitempty"`
			Metadata         map[string]any `json:"metadata,omitempty"`
			Deleted          *bool          `json:"deleted,omitempty"`
		}{OperationID: "fixed-old-operation", ExpectedRevision: 7}
		if present {
			old.Metadata = map[string]any{}
		}
		typedHash, err := mutationRequestHash(struct {
			ID    string
			Input any
		}{"old-item", old})
		if err != nil {
			t.Fatal(err)
		}
		want, err := mutationRequestHash(struct {
			TypedRequestHash string `json:"typed_request_hash"`
			MetadataPresent  bool   `json:"metadata_present"`
		}{typedHash, present})
		if err != nil {
			t.Fatal(err)
		}
		current := LiteraturePatch{OperationID: old.OperationID, ExpectedRevision: old.ExpectedRevision, Metadata: old.Metadata}
		identity, err := literaturePatchRequestIdentity("old-item", current, "{}")
		if err != nil || identity.requestHash() != "v2:"+want || identity.legacyExact != typedHash {
			t.Fatalf("old presence/hash changed: present=%t %v", present, err)
		}
	}
	if (literatureRequestIdentity{}).matches(strings.Repeat("a", 64)) {
		t.Fatal("missing legacy callback acquired an alias")
	}
}
