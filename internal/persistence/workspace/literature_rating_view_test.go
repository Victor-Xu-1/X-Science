package workspace

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func ratingViewJSON(t *testing.T, item LiteratureItem) map[string]any {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil {
		t.Fatal("invalid item JSON")
	}
	view, ok := object["rating_view"].(map[string]any)
	if !ok {
		t.Fatalf("live owner read needs a canonical rating projection, got %s", raw)
	}
	return view
}

func TestLiteratureRatingProjectionUsesExactStoredMetadata(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rating.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for index, test := range []struct {
		metadata string
		expected map[string]any
	}{
		{`{}`, map[string]any{"known": true, "value": json.Number("0")}},
		{`{"rating":0}`, map[string]any{"known": true, "value": json.Number("0")}},
		{`{"rating":-0.0e999999999999999999}`, map[string]any{"known": true, "value": json.Number("0")}},
		{`{"rating":4.0}`, map[string]any{"known": true, "value": json.Number("4")}},
		{`{"rating":5e0}`, map[string]any{"known": true, "value": json.Number("5")}},
		{`{"rating":4.999999999999999999999}`, map[string]any{"known": false}},
		{`{"rating":5.000000000000000000001}`, map[string]any{"known": false}},
		{`{"rating":1e400}`, map[string]any{"known": false}},
		{`{"rating":1e-9999999999999999999}`, map[string]any{"known": false}},
		{`{"rating":6}`, map[string]any{"known": false}},
		{`{"rating":-1}`, map[string]any{"known": false}},
		{`{"rating":"5"}`, map[string]any{"known": false}},
		{`{"rating":null}`, map[string]any{"known": false}},
		{`{"rating":true}`, map[string]any{"known": false}},
		{`{"rating":[5]}`, map[string]any{"known": false}},
		{`{"rating":{"value":5}}`, map[string]any{"known": false}},
		{`{"Rating":5}`, map[string]any{"known": true, "value": json.Number("0")}},
	} {
		id := "rating-" + string(rune('a'+index))
		if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `INSERT INTO literature_items(id,owner_user_id,title,metadata_json,created_at,updated_at) VALUES(?,'owner','Rating',?,'2026-01-01','2026-01-01')`, id, test.metadata)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		item, err := store.GetLiteratureItem(t.Context(), "owner", id)
		if err != nil {
			t.Fatal(err)
		}
		if got := ratingViewJSON(t, item); !reflect.DeepEqual(got, test.expected) {
			t.Fatalf("exact rating %s: got%v want%v", test.metadata, got, test.expected)
		}
		var stored string
		if err := store.db.QueryRowContext(t.Context(), `SELECT metadata_json FROM literature_items WHERE id=?`, id).Scan(&stored); err != nil || stored != test.metadata {
			t.Fatalf("projection rewrote opaque metadata: %s %v", stored, err)
		}
		if _, err := store.GetLiteratureItem(t.Context(), "another-owner", id); !errors.Is(err, ErrLiteratureUnavailable) {
			t.Fatalf("rating read crossed owner: %v", err)
		}
	}
}

func TestLiteratureRatingFieldPatchReplayAndLegacyProjectionOmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rating-cas.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	first, err := store.CreateManualLiterature(t.Context(), "owner", LiteratureManualCreate{OperationID: "rating-create", Title: "Rated reference", Metadata: map[string]any{"rating": json.Number("2"), "opaque": json.Number("1e400"), "personal_note": "Keep note"}})
	if err != nil {
		t.Fatal(err)
	}
	patch := LiteraturePatch{OperationID: "rating-set", ExpectedRevision: first.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"rating":5}}`)}
	ack, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ratingViewJSON(t, ack.Item), map[string]any{"known": true, "value": json.Number("5")}) || ack.Item.Metadata["opaque"] != json.Number("1e400") || ack.Item.Metadata["personal_note"] != "Keep note" {
		t.Fatal("rating patch replaced unedited values")
	}
	changed, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, LiteraturePatch{OperationID: "rating-clear", ExpectedRevision: ack.Item.MetadataRevision, MetadataPatch: json.RawMessage(`{"set":{"rating":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ratingViewJSON(t, changed.Item), map[string]any{"known": true, "value": json.Number("0")}) {
		t.Fatal("zero is the real unrated score")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, patch)
	if err != nil || !reflect.DeepEqual(replay, ack) {
		t.Fatalf("immutable historical rating ACK changed: %+v %v", replay, err)
	}
	live, err := store.GetLiteratureItem(t.Context(), "owner", first.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ratingViewJSON(t, live), map[string]any{"known": true, "value": json.Number("0")}) {
		t.Fatal("historical ACK replaced current rating")
	}
	conflict := patch
	conflict.OperationID = "rating-stale"
	if _, err := store.PatchLiteratureItem(t.Context(), "owner", first.Item.ID, conflict); !errors.Is(err, ErrLiteratureConflict) {
		t.Fatalf("rating bypassed original CAS: %v", err)
	}
	legacy := []byte(`{"schema":"x-science.literature-receipt.v1","operation_id":"legacy","item":{"id":"legacy-item","title":"Legacy","metadata_revision":1,"metadata":{"rating":1e400},"deleted":false,"project_ids":[],"attachments":[]}}`)
	var old LiteratureReceipt
	if err := json.Unmarshal(legacy, &old); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"rating_view"`)) || old.Item.Metadata["rating"] != json.Number("1e400") {
		t.Fatal("legacy ACK omission was recomputed or rounded")
	}
	for _, view := range []string{`{"known":true}`, `{"known":true,"value":6}`, `{"known":true,"value":4.9999999999999999999}`, `{"known":false,"value":0}`, `{"known":true,"value":"5"}`} {
		malformed := bytes.Replace(legacy, []byte(`"deleted":false`), []byte(`"rating_view":`+view+`,"deleted":false`), 1)
		if json.Unmarshal(malformed, &old) == nil {
			t.Fatalf("malformed canonical view accepted: %s", view)
		}
	}
}
