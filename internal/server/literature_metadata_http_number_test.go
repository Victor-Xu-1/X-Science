package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	workspace "x-science/internal/persistence/workspace"
)

type literatureHTTPNumberFixture struct {
	f      *agentSaveArtifactsFixture
	mode   string
	input  workspace.LiteratureImport
	itemID string
}

func newLiteratureHTTPNumberFixture(t *testing.T, mode string) *literatureHTTPNumberFixture {
	t.Helper()
	f, _, source := newPDFReadingFixture(t)
	fixture := &literatureHTTPNumberFixture{f: f, mode: mode, input: workspace.LiteratureImport{
		OperationID: uuid.NewString(), ArtifactID: source.Sources[0].ArtifactID,
		VersionID: source.Sources[0].VersionID, Title: "Numeric metadata fixture", PageCount: 2,
	}}
	if mode == "patch" {
		seed := fixture.input
		seed.OperationID = uuid.NewString()
		receipt, err := f.store.ImportLiteratureArtifact(t.Context(), f.stream.OwnerID, seed)
		if err != nil {
			t.Fatal(err)
		}
		fixture.itemID = receipt.Item.ID
	}
	return fixture
}

func literatureHTTPNumberMetadata(number any) map[string]any {
	return map[string]any{
		"unknown_numeric_field": number,
		"nested":                map[string]any{"values": []any{number, map[string]any{"number": number}}},
		"unchanged_string":      "9007199254740993", "boolean": true, "null": nil,
	}
}

func literatureHTTPNumberJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func literatureHTTPNumberCanonical(t *testing.T, raw []byte) []byte {
	t.Helper()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return literatureHTTPNumberJSON(t, value)
}

func (f *literatureHTTPNumberFixture) command(number any) any {
	metadata := literatureHTTPNumberMetadata(number)
	if f.mode == "patch" {
		return workspace.LiteraturePatch{OperationID: f.input.OperationID, ExpectedRevision: 1, Metadata: metadata}
	}
	input := f.input
	input.Metadata = metadata
	return input
}

func (f *literatureHTTPNumberFixture) request(t *testing.T, input any) *httptest.ResponseRecorder {
	t.Helper()
	method, path := http.MethodPost, "/api/literature/import-artifact-version"
	if f.mode == "patch" {
		method, path = http.MethodPatch, "/api/literature/items/"+f.itemID
	}
	// RawMessage carries the exact JSON tokens through the existing real HTTP
	// helper; no test-side interface/float decode occurs before the request.
	return p3JSONRequest(t, f.f.server, method, path, json.RawMessage(literatureHTTPNumberJSON(t, input)), f.f.stream.OwnerID)
}

func literatureHTTPNumberReceipt(t *testing.T, response *httptest.ResponseRecorder) workspace.LiteratureReceipt {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("Library HTTP%d: %s", response.Code, response.Body.String())
	}
	var receipt workspace.LiteratureReceipt
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func (f *literatureHTTPNumberFixture) ledger(t *testing.T) (string, string) {
	t.Helper()
	var hash, result string
	err := f.f.db.QueryRowContext(t.Context(), `SELECT request_hash,result_json FROM mutation_result_ledger
		WHERE owner_user_id=? AND idempotency_key=? AND operation=?`,
		f.f.stream.OwnerID, f.input.OperationID, "literature."+f.mode).Scan(&hash, &result)
	if err != nil {
		t.Fatal(err)
	}
	return hash, result
}

func (f *literatureHTTPNumberFixture) exactIdentity(t *testing.T, input any) string {
	t.Helper()
	if patch, ok := input.(workspace.LiteraturePatch); ok {
		input = struct {
			ID    string
			Input workspace.LiteraturePatch
		}{f.itemID, patch}
	}
	// The existing typed json.Marshal + full SHA256 contract, not a second
	// production matcher. PageCount retains its original json:"-" exclusion.
	digest := sha256.Sum256(literatureHTTPNumberJSON(t, input))
	return hex.EncodeToString(digest[:])
}

func (f *literatureHTTPNumberFixture) v2Identity(t *testing.T, input any) string {
	t.Helper()
	exact := f.exactIdentity(t, input)
	if patch, ok := input.(workspace.LiteraturePatch); ok {
		envelope := struct {
			TypedRequestHash string `json:"typed_request_hash"`
			MetadataPresent  bool   `json:"metadata_present"`
		}{exact, patch.Metadata != nil}
		digest := sha256.Sum256(literatureHTTPNumberJSON(t, envelope))
		exact = hex.EncodeToString(digest[:])
	}
	return "v2:" + exact
}

func (f *literatureHTTPNumberFixture) commitLegacyFixture(t *testing.T, input any) workspace.LiteratureReceipt {
	t.Helper()
	var receipt workspace.LiteratureReceipt
	var err error
	switch value := input.(type) {
	case workspace.LiteratureImport:
		receipt, err = f.f.store.ImportLiteratureArtifact(t.Context(), f.f.stream.OwnerID, value)
	case workspace.LiteraturePatch:
		receipt, err = f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, value)
	default:
		t.Fatal("unsupported fixture command")
	}
	if err != nil {
		t.Fatal(err)
	}
	expectedRevision := int64(1)
	if f.mode == "patch" {
		expectedRevision = 2
	}
	if receipt.OperationID != f.input.OperationID || receipt.Item.ID == "" || receipt.Item.MetadataRevision != expectedRevision {
		t.Fatalf("actual seed mutation did not return its original operation/revision: %#v", receipt)
	}
	stored, originalACK := f.ledger(t)
	legacy := f.exactIdentity(t, input)
	if stored != legacy && stored != f.v2Identity(t, input) {
		t.Fatalf("unexpected committed encoding %q", stored)
	}
	// Only this newly committed temporary fixture row is placed into the
	// historical unmarked format. Its actual successful ACK/business effects
	// are not synthesized, re-encoded, overwritten or migrated.
	result, err := f.f.db.ExecContext(t.Context(), `UPDATE mutation_result_ledger SET request_hash=?
		WHERE owner_user_id=? AND idempotency_key=? AND operation=? AND request_hash=?`,
		legacy, f.f.stream.OwnerID, f.input.OperationID, "literature."+f.mode, stored)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("legacy fixture affected %d rows: %v", count, err)
	}
	if hash, ack := f.ledger(t); hash != legacy || ack != originalACK {
		t.Fatal("legacy fixture changed the original ACK")
	}
	return receipt
}

func TestLiteratureHTTPPreciseMetadataNumbers(t *testing.T) {
	for _, mode := range []string{"import", "patch"} {
		for _, number := range []string{"9007199254740993", "0.12345678901234567890123456789", "1e400"} {
			t.Run(mode+"/"+number, func(t *testing.T) {
				f := newLiteratureHTTPNumberFixture(t, mode)
				input := f.command(json.Number(number))
				response := f.request(t, input)
				receipt := literatureHTTPNumberReceipt(t, response)
				expected := literatureHTTPNumberJSON(t, literatureHTTPNumberMetadata(json.Number(number)))
				if actual := literatureHTTPNumberJSON(t, receipt.Item.Metadata); !bytes.Equal(actual, expected) {
					t.Errorf("HTTP ACK rounded opaque metadata: got %s want %s", actual, expected)
				}
				current, err := f.f.store.GetLiteratureItem(t.Context(), f.f.stream.OwnerID, receipt.Item.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(literatureHTTPNumberJSON(t, current.Metadata), expected) {
					t.Error("canonical Store read lost HTTP metadata precision")
				}
				list := p3JSONRequest(t, f.f.server, http.MethodGet, "/api/literature/items", nil, f.f.stream.OwnerID)
				var page workspace.LiteraturePage
				decoder := json.NewDecoder(bytes.NewReader(list.Body.Bytes()))
				decoder.UseNumber()
				if list.Code != 200 || decoder.Decode(&page) != nil || len(page.Items) != 1 || page.Items[0].ID != receipt.Item.ID {
					t.Fatalf("canonical HTTP list failed: %d %s", list.Code, list.Body.String())
				}
				if !bytes.Equal(literatureHTTPNumberJSON(t, page.Items[0].Metadata), expected) {
					t.Error("HTTP listing lost precise metadata")
				}
				replay := f.request(t, input)
				literatureHTTPNumberReceipt(t, replay)
				if !bytes.Equal(literatureHTTPNumberCanonical(t, replay.Body.Bytes()), literatureHTTPNumberCanonical(t, response.Body.Bytes())) {
					t.Error("same precise request did not replay the original ACK")
				}
			})
		}
	}
}

func TestLiteratureHTTPPreciseRequestCannotBorrowACK(t *testing.T) {
	for _, mode := range []string{"import", "patch"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiteratureHTTPNumberFixture(t, mode)
			original := f.command(json.Number("9007199254740992"))
			response := f.request(t, original)
			literatureHTTPNumberReceipt(t, response)
			hash, ack := f.ledger(t)
			if hash != f.v2Identity(t, original) {
				t.Errorf("new Library request lacks exact v2 identity: %q", hash)
			}
			// These exact integers share the old binary64 projection. New v2
			// requests must conflict, never borrow the committed original ACK.
			changed := f.request(t, f.command(json.Number("9007199254740993")))
			if changed.Code != http.StatusConflict {
				t.Errorf("changed precise request borrowed ACK: HTTP%d %s", changed.Code, changed.Body.String())
			}
			replay := f.request(t, original)
			literatureHTTPNumberReceipt(t, replay)
			if !bytes.Equal(literatureHTTPNumberCanonical(t, replay.Body.Bytes()), literatureHTTPNumberCanonical(t, []byte(ack))) {
				t.Error("unchanged request lost its original ACK")
			}
			if afterHash, afterACK := f.ledger(t); afterHash != hash || afterACK != ack {
				t.Error("collision/replay rewrote the original ledger row")
			}
		})
	}
}

func TestLiteratureHTTPLegacyNumberACKRemainsImmutable(t *testing.T) {
	cases := []struct {
		name, wire string
		exact      bool
	}{
		{"rounded legacy integer", "9007199254740993", false},
		{"legacy lexical projection", "1.0", false},
		{"exact legacy without float projection", "1e400", true},
	}
	for _, mode := range []string{"import", "patch"} {
		for _, test := range cases {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				f := newLiteratureHTTPNumberFixture(t, mode)
				var projected any
				projectionErr := json.Unmarshal([]byte(test.wire), &projected)
				seedNumber := projected
				if test.exact {
					if projectionErr == nil {
						t.Fatal("fixture requires an unavailable legacy float projection")
					}
					seedNumber = json.Number(test.wire)
				} else if projectionErr != nil {
					t.Fatal(projectionErr)
				}
				seed := f.command(seedNumber)
				receipt := f.commitLegacyFixture(t, seed)
				hash, ack := f.ledger(t)
				laterTitle := "Later independently committed title"
				later, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, receipt.Item.ID, workspace.LiteraturePatch{
					OperationID: uuid.NewString(), ExpectedRevision: receipt.Item.MetadataRevision,
					Title: &laterTitle, Metadata: literatureHTTPNumberMetadata(json.Number("42")),
				})
				if err != nil {
					t.Fatal(err)
				}
				replay := f.request(t, f.command(json.Number(test.wire)))
				literatureHTTPNumberReceipt(t, replay)
				if !bytes.Equal(literatureHTTPNumberCanonical(t, replay.Body.Bytes()), literatureHTTPNumberCanonical(t, []byte(ack))) {
					t.Error("legacy retry did not return its immutable original ACK")
				}
				if afterHash, afterACK := f.ledger(t); afterHash != hash || afterACK != ack {
					t.Error("legacy replay rewrote the old hash or ACK")
				}
				current, err := f.f.store.GetLiteratureItem(t.Context(), f.f.stream.OwnerID, receipt.Item.ID)
				if err != nil || current.MetadataRevision != later.Item.MetadataRevision || current.Title != laterTitle {
					t.Fatalf("replay repeated a mutation or lost current state: %#v err=%v", current, err)
				}
			})
		}
	}
}

func TestLiteratureHTTPPatchMetadataPresenceCannotBorrowACK(t *testing.T) {
	f := newLiteratureHTTPNumberFixture(t, "patch")
	metadata := map[string]any{"retained_field": "clear only when explicitly requested"}
	seeded, err := f.f.store.PatchLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID, workspace.LiteraturePatch{
		OperationID: uuid.NewString(), ExpectedRevision: 1, Metadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	title := "Independent title-only metadata-presence command"
	original := workspace.LiteraturePatch{
		OperationID: f.input.OperationID, ExpectedRevision: seeded.Item.MetadataRevision, Title: &title, Metadata: nil,
	}
	first := literatureHTTPNumberReceipt(t, f.request(t, original))
	if first.Item.MetadataRevision != seeded.Item.MetadataRevision+1 ||
		!bytes.Equal(literatureHTTPNumberJSON(t, first.Item.Metadata), literatureHTTPNumberJSON(t, metadata)) {
		t.Fatalf("title-only command did not preserve seeded metadata: %#v", first)
	}
	hash, ack := f.ledger(t)
	// A typed empty map would be omitted by json:",omitempty" in the helper.
	// Keep the real HTTP distinction: absent metadata versus an explicit {}.
	changed := map[string]any{
		"operation_id": f.input.OperationID, "expected_revision": seeded.Item.MetadataRevision,
		"title": title, "metadata": map[string]any{},
	}
	collision := f.request(t, changed)
	if collision.Code != http.StatusConflict {
		t.Errorf("explicit metadata clear borrowed title-only ACK: HTTP%d %s", collision.Code, collision.Body.String())
	}
	if afterHash, afterACK := f.ledger(t); afterHash != hash || afterACK != ack {
		t.Error("changed metadata presence rewrote the original raw ledger ACK")
	}
	current, err := f.f.store.GetLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID)
	if err != nil || current.MetadataRevision != first.Item.MetadataRevision || current.Title != title ||
		!bytes.Equal(literatureHTTPNumberJSON(t, current.Metadata), literatureHTTPNumberJSON(t, metadata)) {
		t.Fatalf("same-operation conflict changed the current item: %#v err=%v", current, err)
	}
	// A genuinely different command proves that {} means clear, not no-op.
	changed["operation_id"], changed["expected_revision"] = uuid.NewString(), current.MetadataRevision
	cleared := literatureHTTPNumberReceipt(t, f.request(t, changed))
	if cleared.Item.MetadataRevision != current.MetadataRevision+1 || len(cleared.Item.Metadata) != 0 {
		t.Fatalf("independent explicit clear did not produce its actual clearing ACK: %#v", cleared)
	}
	readback, err := f.f.store.GetLiteratureItem(t.Context(), f.f.stream.OwnerID, f.itemID)
	if err != nil || readback.MetadataRevision != cleared.Item.MetadataRevision || len(readback.Metadata) != 0 {
		t.Fatalf("explicit empty metadata did not clear the stored object: %#v err=%v", readback, err)
	}
	if afterHash, afterACK := f.ledger(t); afterHash != hash || afterACK != ack {
		t.Error("independent clear rewrote the original request hash or raw ACK")
	}
}
