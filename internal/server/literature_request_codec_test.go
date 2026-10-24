package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
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
