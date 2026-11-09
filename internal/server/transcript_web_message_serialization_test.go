package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebMessageRecordSerializationParity(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		json  string
	}{
		{"nil_map", map[string]any(nil), "null"},
		{"empty_map", map[string]any{}, "{}"},
		{"nil_any_slice", []any(nil), "[]"},
		{"empty_any_slice", []any{}, "[]"},
		{"nil_map_slice", []map[string]any(nil), "[]"},
		{"empty_map_slice", []map[string]any{}, "[]"},
		{"nil_strings", []string(nil), "null"},
		{"empty_strings", []string{}, "null"},
		{"strings", []string{"synthetic"}, `["synthetic"]`},
		{"nil_bytes", []byte(nil), "null"},
		{"empty_bytes", []byte{}, "null"},
		{"bytes", []byte{0, 1}, `"AAE="`},
		{"nested", []any{map[string]any{"empty": []string{}, "nil": []any(nil)}}, `[{"empty":null,"nil":[]}]`},
		{"decimal_number", json.Number("1.2300"), "1.2300"},
		{"large_integer", json.Number("9007199254740993"), "9007199254740993"},
		{"negative_zero", math.Copysign(0, -1), "-0"},
		{"raw_object", json.RawMessage(` { "n": 1 } `), `{"n":1}`},
		{"raw_scalar", json.RawMessage(`"synthetic"`), `"synthetic"`},
		{"raw_null", json.RawMessage(`null`), "null"},
		{"raw_nil", json.RawMessage(nil), "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, metadata := transcriptWebSerializationFixture(0, 1)
			message["content"] = map[string]any{"value": test.value, "artifact_refs": "nested-preserved"}
			before, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			want, err := legacyTranscriptWebRecordJSON(message)
			if err != nil {
				t.Fatal(err)
			}
			records, references, err := buildTranscriptWebMessageRecords([]map[string]any{message}, metadata)
			if err != nil || len(records) != 1 || !bytes.Equal(records[0].MessageJSON, want) ||
				records[0].MessageSHA256 != transcriptstore.TranscriptWebSHA256(want) {
				t.Fatalf("record changed original encoding or SHA: records=%d err=%v", len(records), err)
			}
			var envelope map[string]json.RawMessage
			var content map[string]json.RawMessage
			if err := json.Unmarshal(records[0].MessageJSON, &envelope); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(envelope["content"], &content); err != nil {
				t.Fatal(err)
			}
			if _, found := envelope["artifact_refs"]; found || string(content["value"]) != test.json ||
				string(content["artifact_refs"]) != `"nested-preserved"` {
				t.Fatalf("fixed JSON shape or top-level-only omission changed: %s", records[0].MessageJSON)
			}
			wantReferences := []transcriptstore.TranscriptWebMessageArtifactReference{{
				MessageOrdinal: 1, Ordinal: 0, ArtifactID: "artifact-000", VersionID: "version-000",
				Relation: transcriptstore.ArtifactRelationAttached, SourceEventID: 1, SourceReferenceOrdinal: 0,
				Filename: "attachment-000.txt", ContentType: "text/plain", SizeBytes: 1024, Checksum: strings.Repeat("a", 64),
			}}
			if !reflect.DeepEqual(references, wantReferences) || !reflect.DeepEqual(records[0].ArtifactReferences, wantReferences) {
				t.Fatal("artifact references changed during message serialization")
			}
			after, err := json.Marshal(message)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("serialization mutated its input")
			}
		})
	}
	t.Run("no_references", func(t *testing.T) {
		message, metadata := transcriptWebSerializationFixture(32, 0)
		delete(message, "artifact_refs")
		want, err := legacyTranscriptWebRecordJSON(message)
		if err != nil {
			t.Fatal(err)
		}
		records, references, err := buildTranscriptWebMessageRecords([]map[string]any{message}, metadata)
		if err != nil || len(records) != 1 || !bytes.Equal(records[0].MessageJSON, want) || len(references) != 0 {
			t.Fatalf("message without artifact_refs changed: %v", err)
		}
	})
}

type transcriptWebSerializationMarshaler struct {
	raw   string
	err   error
	calls *int
}

func (value transcriptWebSerializationMarshaler) MarshalJSON() ([]byte, error) {
	if value.calls != nil {
		(*value.calls)++
	}
	return []byte(value.raw), value.err
}

func TestTranscriptWebMessageRecordSerializationErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
	}{
		{"empty_raw", json.RawMessage{}},
		{"invalid_raw", json.RawMessage(`{`)},
		{"invalid_marshaler", transcriptWebSerializationMarshaler{raw: `{`}},
		{"failed_marshaler", transcriptWebSerializationMarshaler{err: errors.New("synthetic encoding failure")}},
		{"nan", math.NaN()},
		{"positive_infinity", math.Inf(1)},
		{"negative_infinity", math.Inf(-1)},
		{"invalid_number", json.Number("not-a-number")},
		{"unsupported", func() {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, metadata := transcriptWebSerializationFixture(0, 0)
			message["content"] = map[string]any{"value": test.value}
			if _, err := legacyTranscriptWebRecordJSON(message); !errors.Is(err, transcriptstore.ErrEventConflict) {
				t.Fatalf("legacy oracle did not reject fixture: %v", err)
			}
			records, references, err := buildTranscriptWebMessageRecords([]map[string]any{message}, metadata)
			if !errors.Is(err, transcriptstore.ErrEventConflict) || records != nil || references != nil {
				t.Fatalf("serialization error boundary changed: records=%d references=%d err=%v", len(records), len(references), err)
			}
		})
	}
	t.Run("marshaler_once", func(t *testing.T) {
		message, metadata := transcriptWebSerializationFixture(0, 0)
		calls := 0
		message["content"] = transcriptWebSerializationMarshaler{raw: `{"synthetic":true}`, calls: &calls}
		records, _, err := buildTranscriptWebMessageRecords([]map[string]any{message}, metadata)
		if err != nil || len(records) != 1 || calls != 1 {
			t.Fatalf("valid marshaler calls=%d err=%v", calls, err)
		}
		calls = 0
		want, err := legacyTranscriptWebRecordJSON(message)
		if err != nil || calls != 1 || !bytes.Equal(records[0].MessageJSON, want) {
			t.Fatalf("valid marshaler differs from original: calls=%d err=%v", calls, err)
		}
	})
	t.Run("invalid_references_precede_serialization", func(t *testing.T) {
		message, metadata := transcriptWebSerializationFixture(0, 0)
		calls := 0
		message["content"] = transcriptWebSerializationMarshaler{raw: `{"synthetic":true}`, calls: &calls}
		message["artifact_refs"] = []map[string]any{{"artifact_id": "incomplete"}}
		_, _, err := buildTranscriptWebMessageRecords([]map[string]any{message}, metadata)
		if !errors.Is(err, transcriptstore.ErrEventConflict) || calls != 0 {
			t.Fatalf("artifact validation moved after encoding: calls=%d err=%v", calls, err)
		}
	})
}
