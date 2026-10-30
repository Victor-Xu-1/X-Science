package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func TestLiteratureMutationDecoderPreservesStrictEnvelope(t *testing.T) {
	for _, test := range []struct {
		name, body, query string
		invalid           bool
	}{
		{name: "precise nested numbers", body: `{"metadata":{"integer":9007199254740993,"nested":[1e400]}}`},
		{name: "trailing whitespace", body: "{\"metadata\":{}} \n\t"},
		{name: "unknown field", body: `{"unexpected":true}`, invalid: true},
		{name: "query", body: `{}`, query: "?unaccepted=1", invalid: true},
		{name: "second object", body: `{} {}`, invalid: true},
		{name: "trailing garbage", body: `{} x`, invalid: true},
		{name: "invalid number", body: `{"metadata":{"number":01}}`, invalid: true},
		{name: "body limit", body: `{"metadata":{"padding":"` + strings.Repeat("x", 64<<10) + `"}}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/api/literature/import"+test.query, strings.NewReader(test.body))
			var value struct {
				Metadata map[string]any `json:"metadata"`
			}
			err := decodeLiteratureMutationBody(httptest.NewRecorder(), request, &value)
			if (err != nil) != test.invalid {
				t.Fatalf("decode error=%v invalid=%t", err, test.invalid)
			}
			if test.name == "precise nested numbers" && err == nil {
				integer, ok := value.Metadata["integer"].(json.Number)
				if !ok || integer.String() != "9007199254740993" {
					t.Fatalf("integer lost exact number type/value: %#v", value.Metadata)
				}
				nested, ok := value.Metadata["nested"].([]any)
				if !ok || len(nested) != 1 {
					t.Fatalf("nested numbers missing: %#v", value.Metadata)
				}
				large, ok := nested[0].(json.Number)
				if !ok || large.String() != "1e400" {
					t.Fatalf("large finite JSON number rejected or rounded: %#v", nested)
				}
			}
		})
	}
}

func TestLiteratureMutationDecoderLargeCatalogEnvelope(t *testing.T) {
	metadata := map[string]any{"abstract": strings.Repeat("a", 16000), "personal_note": strings.Repeat("\x01", 10000)}
	for _, value := range []any{
		&workspace.LiteraturePatch{}, &workspace.LiteratureImport{}, &workspace.LiteratureManualCreate{},
	} {
		body, err := json.Marshal(map[string]any{"metadata": metadata})
		if err != nil || len(body) <= 64<<10 || len(body) >= 128<<10 {
			t.Fatalf("fixture must exercise only the expanded bounded catalog receiver: bytes=%d err=%v", len(body), err)
		}
		request := httptest.NewRequest("POST", "/api/literature/items", strings.NewReader(string(body)))
		if err := decodeLiteratureMutationBody(httptest.NewRecorder(), request, value); err != nil {
			t.Fatalf("original large catalog request could not reach its ledger receiver: %T %v", value, err)
		}
		oversized := `{"metadata":{"padding":"` + strings.Repeat("x", 128<<10) + `"}}`
		request = httptest.NewRequest("POST", "/api/literature/items", strings.NewReader(oversized))
		if err := decodeLiteratureMutationBody(httptest.NewRecorder(), request, value); err == nil {
			t.Fatalf("catalog envelope lost its finite byte ceiling: %T", value)
		}
	}
}
