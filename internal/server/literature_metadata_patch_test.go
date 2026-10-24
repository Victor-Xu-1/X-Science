package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

const literatureMetadataPatchSeed = `{"authors":["Ada","Grace"],"year":2024,"opaque":{"integer":9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1e400,"array":[1e400,{"n":9007199254740993}]},"flag":false,"nullable":null}`

func seedLiteratureMetadataPatch(t *testing.T, f *literatureHTTPNumberFixture) workspace.LiteratureReceipt {
	t.Helper()
	var metadata map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(literatureMetadataPatchSeed))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	seeded, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1, Metadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	return seeded
}

// This regression deliberately uses only the original main22 Go API/types.
// Its new contract is carried as raw HTTP JSON, so the old source compiles
// and fails on behavior (HTTP400), not on a reference to an unimplemented type.
func TestLiteratureHTTPMetadataPatchPreservesCanonicalNumbers(t *testing.T) {
	for _, test := range []struct{ field, value string }{
		{field: "authors", value: "Edited authors"},
		{field: "year", value: "2025"},
	} {
		t.Run(test.field, func(t *testing.T) {
			f := newLiteratureHTTPNumberFixture(t, "patch")
			seeded := seedLiteratureMetadataPatch(t, f)
			command := map[string]any{
				"operation_id": f.input.OperationID, "expected_revision": seeded.Item.MetadataRevision,
				"metadata_patch": map[string]any{"set": map[string]any{test.field: test.value}},
			}
			response := f.request(t, command)
			if response.Code != http.StatusOK {
				t.Fatalf("metadata field edit was not accepted through the original PATCH: HTTP%d %s", response.Code, response.Body.String())
			}
			receipt := literatureHTTPNumberReceipt(t, response)
			expected := make(map[string]any, len(seeded.Item.Metadata))
			for key, value := range seeded.Item.Metadata {
				expected[key] = value
			}
			expected[test.field] = test.value
			want := literatureHTTPNumberJSON(t, expected)
			if receipt.OperationID != f.input.OperationID || receipt.Item.MetadataRevision != seeded.Item.MetadataRevision+1 ||
				!bytes.Equal(literatureHTTPNumberJSON(t, receipt.Item.Metadata), want) {
				t.Fatalf("field edit changed unedited canonical values or its revision: %#v", receipt)
			}
			var raw string
			if err := f.f.db.QueryRowContext(t.Context(), `SELECT metadata_json FROM literature_items WHERE id=? AND owner_user_id=?`,
				f.itemID, f.f.stream.OwnerID).Scan(&raw); err != nil || !bytes.Equal([]byte(raw), want) {
				t.Fatalf("persisted metadata lost original number tokens: %s want=%s err=%v", raw, want, err)
			}
			pageResponse := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items", nil, f.f.stream.OwnerID)
			var page workspace.LiteraturePage
			decoder := json.NewDecoder(bytes.NewReader(pageResponse.Body.Bytes()))
			decoder.UseNumber()
			if pageResponse.Code != http.StatusOK || decoder.Decode(&page) != nil || len(page.Items) != 1 ||
				page.Items[0].ID != f.itemID || !bytes.Equal(literatureHTTPNumberJSON(t, page.Items[0].Metadata), want) {
				t.Fatalf("GET did not read the same precise edited object: HTTP%d %s", pageResponse.Code, pageResponse.Body.String())
			}
			hash, ack := f.ledger(t)
			replay := f.request(t, command)
			if replay.Code != http.StatusOK ||
				!bytes.Equal(literatureHTTPNumberCanonical(t, replay.Body.Bytes()), literatureHTTPNumberCanonical(t, response.Body.Bytes())) {
				t.Fatalf("original field edit did not replay its exact ACK: HTTP%d %s", replay.Code, replay.Body.String())
			}
			if afterHash, afterACK := f.ledger(t); afterHash != hash || afterACK != ack {
				t.Fatal("metadata field replay rewrote the original ledger")
			}
		})
	}
}

func TestLiteratureHTTPMetadataPatchRejectsAmbiguousPresence(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	seeded := seedLiteratureMetadataPatch(t, f)
	before := literatureHTTPNumberJSON(t, seeded.Item.Metadata)
	var countBefore int
	if err := f.f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=?`, f.f.stream.OwnerID).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, patch string
		extra       map[string]any
	}{
		{name: "patch-null", patch: `null`},
		{name: "empty", patch: `{}`},
		{name: "no-actions", patch: `{"set":{},"remove":[]}`},
		{name: "set-null", patch: `{"set":null}`},
		{name: "remove-null", patch: `{"remove":null}`},
		{name: "remove-null-key", patch: `{"remove":[null]}`},
		{name: "wrong-set-type", patch: `{"set":[]}`},
		{name: "wrong-remove-type", patch: `{"remove":"authors"}`},
		{name: "overlap", patch: `{"set":{"authors":"Edited"},"remove":["authors"]}`},
		{name: "duplicate-remove", patch: `{"remove":["authors","authors"]}`},
		{name: "unknown-operator", patch: `{"replace":{"authors":"Edited"}}`},
		{name: "wrong-operator-case", patch: `{"Set":{"authors":"Edited"}}`},
		{name: "full-object", patch: `{"set":{"authors":"Edited"}}`, extra: map[string]any{"metadata": map[string]any{}}},
		{name: "deleted-false", patch: `{"set":{"authors":"Edited"}}`, extra: map[string]any{"deleted": false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := map[string]any{
				"operation_id": uuid.NewString(), "expected_revision": seeded.Item.MetadataRevision,
				"metadata_patch": json.RawMessage(test.patch),
			}
			for key, value := range test.extra {
				command[key] = value
			}
			response := f.request(t, command)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("ambiguous field delta was accepted: HTTP%d %s", response.Code, response.Body.String())
			}
		})
	}
	current, err := f.f.store.GetLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID)
	var countAfter int
	countErr := f.f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM mutation_result_ledger WHERE owner_user_id=?`, f.f.stream.OwnerID).Scan(&countAfter)
	if err != nil || countErr != nil || countAfter != countBefore || current.MetadataRevision != seeded.Item.MetadataRevision ||
		!bytes.Equal(literatureHTTPNumberJSON(t, current.Metadata), before) {
		t.Fatalf("invalid field input changed catalog/ledger: revision=%d ledger=%d->%d err=%v/%v", current.MetadataRevision, countBefore, countAfter, err, countErr)
	}
	// The old outer fields keep their original nil/null=no-operation meaning.
	// A null is not a full replacement or a deletion and needs no new decoder.
	for _, key := range []string{"metadata", "deleted"} {
		command := map[string]any{
			"operation_id": uuid.NewString(), "expected_revision": current.MetadataRevision,
			"metadata_patch": map[string]any{"set": map[string]any{"compatibility": "accepted"}}, key: nil,
		}
		receipt := literatureHTTPNumberReceipt(t, f.request(t, command))
		expected := make(map[string]any, len(seeded.Item.Metadata)+1)
		for field, value := range seeded.Item.Metadata {
			expected[field] = value
		}
		expected["compatibility"] = "accepted"
		if receipt.Item.MetadataRevision != current.MetadataRevision+1 || !bytes.Equal(literatureHTTPNumberJSON(t, receipt.Item.Metadata), literatureHTTPNumberJSON(t, expected)) {
			t.Fatalf("nil %s lost its original semantics or precise fields", key)
		}
		current = receipt.Item
	}
}
