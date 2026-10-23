package server

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	runtimeMCPConnectorExampleBytes         = 2048
	runtimeMCPConnectorExamplePatternBudget = 25 * time.Millisecond
)

func runtimeMCPConnectorExampleInput(schema map[string]any) string {
	properties, _ := schema["properties"].(map[string]any)
	required := stringArrayValue(schema["required"])
	values := make(map[string]any, len(required))
	for _, name := range required {
		property, _ := properties[name].(map[string]any)
		values[name] = runtimeMCPConnectorExampleValue(property)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func runtimeMCPConnectorDeclaredSample(schema map[string]any) (any, bool) {
	examples, ok := schema["examples"].([]any)
	if !ok || len(examples) == 0 {
		return nil, false
	}
	// This is bounded presentation, never the tool's argument/permission
	// authority. Complex/ref/composed schemas retain their existing templates
	// rather than adding potentially expensive validation during Skill rendering.
	for key := range schema {
		switch key {
		case "type", "enum", "const", "pattern", "minLength", "maxLength",
			"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"default", "examples", "description", "title":
		default:
			return nil, false
		}
	}
	if kind := stringValue(schema["type"]); kind != "string" && kind != "number" &&
		kind != "integer" && kind != "boolean" && kind != "null" {
		return nil, false
	}
	raw, err := json.Marshal(schema)
	if err != nil || len(raw) > 16<<10 {
		return nil, false
	}
	var normalized map[string]any
	if json.Unmarshal(raw, &normalized) != nil {
		return nil, false
	}
	// Same compiler, dialect and no-network loader; only this advisory
	// rendering operation gets its own regexp deadline. Actual input validators
	// retain the unchanged default engine and their existing owner/claim rules.
	compiled, err := compileKernelDraft7SchemaWithRegexp(normalized, compileKernelMCPECMARegexpWithTimeout(runtimeMCPConnectorExamplePatternBudget))
	if err != nil {
		return nil, false
	}
	for _, example := range examples[:min(8, len(examples))] {
		encoded, err := json.Marshal(example)
		if err != nil || len(encoded) > runtimeMCPConnectorExampleBytes {
			continue
		}
		var candidate any
		if json.Unmarshal(encoded, &candidate) == nil && compiled.Validate(candidate) == nil {
			return candidate, true
		}
	}
	return nil, false
}

func runtimeMCPConnectorExampleValue(schema map[string]any) any {
	if value, found := schema["default"]; found {
		return value
	}
	if sample, found := runtimeMCPConnectorDeclaredSample(schema); found {
		return sample
	}
	if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	switch strings.ToLower(strings.TrimSpace(stringValue(schema["type"]))) {
	case "array":
		item, _ := schema["items"].(map[string]any)
		return []any{runtimeMCPConnectorExampleValue(item)}
	case "object":
		return map[string]any{}
	case "integer":
		return 1
	case "number":
		return 1.0
	case "boolean":
		return true
	default:
		return "value"
	}
}
