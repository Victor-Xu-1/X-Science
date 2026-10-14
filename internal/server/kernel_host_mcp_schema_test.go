package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestKernelMCPCatalogInputSchemaPreservesCompleteAdmittedConstraints(t *testing.T) {
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"query"},
		"$defs": map[string]any{"Term": map[string]any{"oneOf": []any{
			map[string]any{"type": "string", "minLength": 1}, map[string]any{"type": "integer"},
		}}},
		"properties": map[string]any{"query": map[string]any{
			"type": "object", "required": []string{"terms"}, "additionalProperties": false,
			"properties": map[string]any{"terms": map[string]any{
				"anyOf": []any{map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Term"}}, map[string]any{"type": "null"}},
			}},
		}},
		"description": strings.Repeat("schema detail ", 6000),
	}
	before, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) <= 64<<10 {
		t.Fatal("fixture does not exceed the old per-schema inline threshold")
	}
	fields := kernelMCPCatalogInputSchemaFields(schema)
	if fields["input_schema_status"] != "complete" || !reflect.DeepEqual(fields["input_schema"], schema) {
		t.Fatal("discovery replaced complete admitted constraints with a projection")
	}
	encoded, err := json.Marshal(fields["input_schema"])
	if err != nil || string(encoded) != string(before) {
		t.Fatal("large input schema was changed or truncated")
	}
	after, err := json.Marshal(schema)
	if err != nil || string(after) != string(before) {
		t.Fatal("discovery mutated the admitted input schema")
	}
}

func TestKernelMCPCatalogInputSchemaDistinguishesUnavailableAndEmptySchema(t *testing.T) {
	for _, schema := range []map[string]any{nil, {}} {
		fields := kernelMCPCatalogInputSchemaFields(schema)
		wantStatus := "complete"
		if schema == nil {
			wantStatus = "unavailable"
		}
		if fields["input_schema_status"] != wantStatus || !reflect.DeepEqual(fields["input_schema"], schema) {
			t.Fatalf("schema availability was fabricated: %#v", fields)
		}
		if _, err := json.Marshal(fields); err != nil {
			t.Fatal(err)
		}
	}
}
