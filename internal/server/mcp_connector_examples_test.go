package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRuntimeMCPConnectorExamplesPreserveValidatedDeclaredSamples(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []string{"accession"},
		"properties": map[string]any{
			"accession": map[string]any{
				"type": "string", "pattern": "^REC-[0-9]{4}$",
				"examples": []any{"invalid", "REC-0042"},
			},
		},
	}
	var example map[string]any
	if err := json.Unmarshal([]byte(runtimeMCPConnectorExampleInput(schema)), &example); err != nil {
		t.Fatal(err)
	}
	if example["accession"] != "REC-0042" {
		t.Fatalf("declared valid example was replaced by an invalid placeholder: %#v", example)
	}
	if err := validateKernelMCPInput(schema, example); err != nil {
		t.Fatalf("generated connector Skill example violates the real input validator: %v", err)
	}
	example["url"] = "https://example.invalid/ignored-filter"
	if err := validateKernelMCPInput(schema, example); err == nil {
		t.Fatal("schema examples must not weaken the shared named-argument boundary")
	}
}

func TestRuntimeMCPConnectorExamplesRespectPatternBudget(t *testing.T) {
	if runtimeMCPConnectorExamplePatternBudget <= 0 || runtimeMCPConnectorExamplePatternBudget > time.Second {
		t.Fatal("advisory schema patterns need a finite, small per-match budget")
	}
	compiled, err := compileKernelMCPECMARegexpWithTimeout(runtimeMCPConnectorExamplePatternBudget)("^REC-[0-9]{4}$")
	if err != nil {
		t.Fatal(err)
	}
	engine, ok := compiled.(*kernelMCPECMARegexp)
	if !ok || engine.expression.MatchTimeout != runtimeMCPConnectorExamplePatternBudget {
		t.Fatal("the advisory regexp engine did not retain its finite match budget")
	}
	if compiled.MatchString("invalid") || !compiled.MatchString("REC-0042") {
		t.Fatal("bounded matching changed normal schema-pattern semantics")
	}
	got, found := runtimeMCPConnectorDeclaredSample(map[string]any{
		"type": "string", "pattern": "^REC-[0-9]{4}$", "examples": []any{"invalid", "REC-0042"},
	})
	if !found || got != "REC-0042" {
		t.Fatalf("normal invalid sample prevented the later valid sample: %#v", got)
	}
}

func TestRuntimeMCPConnectorExamplesKeepExistingFallbacks(t *testing.T) {
	for _, test := range []struct {
		name   string
		schema map[string]any
		want   any
	}{
		{"default", map[string]any{"type": "string", "default": "chosen", "examples": []any{"sample"}}, "chosen"},
		{"invalid-sample", map[string]any{"type": "string", "examples": []any{false}, "enum": []any{"valid"}}, "valid"},
		{"oversized-sample", map[string]any{"type": "string", "examples": []any{strings.Repeat("x", runtimeMCPConnectorExampleBytes+1), "short"}}, "short"},
		{"no-samples", map[string]any{"type": "integer"}, 1},
		{"external-ref", map[string]any{"type": "string", "$ref": "https://example.invalid/schema", "examples": []any{"sample"}}, "value"},
		{"composed-schema", map[string]any{"type": "string", "allOf": []any{map[string]any{"pattern": "^sample$"}}, "examples": []any{"sample"}}, "value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := runtimeMCPConnectorExampleValue(test.schema); got != test.want {
				t.Fatalf("example=%#v want=%#v", got, test.want)
			}
		})
	}
}
