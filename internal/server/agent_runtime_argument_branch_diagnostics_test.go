package server

import (
	"encoding/json"
	"strings"
	"testing"

	"synon-go/internal/agentruntime"
)

func TestArgumentDiagnosticsRespectTheSelectedSchemaBranch(t *testing.T) {
	schema := agentEnvironmentManagementToolSchemas()[0]
	validator := compileAgentRuntimeMCPValidator(schema)
	input := map[string]any{
		"mode": "preflight", "provider": "local-conda", "name": "analysis",
		"human_description":     "Inspect the selected environment",
		"resource_requirements": managedEnvironmentTestResources(),
	}
	result := validator.Validate(input)
	if result == nil {
		t.Fatal("missing branch-required packages were admitted")
	}
	issues, _ := result["issues"].([]map[string]any)
	if len(issues) != 1 || issues[0]["path"] != "/packages" || issues[0]["keyword"] != "required" {
		t.Fatalf("feedback mixed in unrelated schema branches: %#v", issues)
	}
	contract := result["expectedArguments"].(map[string]any)
	fields := contract["fields"].(map[string]any)
	if fields["packages"].(map[string]any)["required"] != true {
		t.Fatalf("selected branch's required field was described as optional: %#v", fields["packages"])
	}
	input["packages"] = []any{"python=3.11"}
	if result := validator.Validate(input); result != nil {
		t.Fatalf("valid branch input was changed by diagnostic projection: %#v", result)
	}
}

func TestArgumentAdmissionDiagnosticIsBoundedCompleteJSON(t *testing.T) {
	schema := agentEnvironmentManagementToolSchemas()[0]
	gateway := serverAgentRuntimeToolGateway{
		server: &Server{}, toolSchemas: []agentruntime.ToolSchema{schema},
		toolValidators:  agentRuntimeToolValidators([]agentruntime.ToolSchema{schema}),
		hasToolSnapshot: true,
	}
	arguments, err := json.Marshal(map[string]any{
		"mode": "preflight", "provider": "local-conda", "name": "analysis",
		"human_description":     "PRIVATE_INPUT_VALUE_MUST_NOT_APPEAR",
		"resource_requirements": managedEnvironmentTestResources(),
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := gateway.ToolCallAdmissionDiagnostic(agentruntime.ToolCall{Name: schema.Name, Arguments: arguments})
	if !json.Valid([]byte(diagnostic)) || len(diagnostic) > 1800 {
		t.Fatalf("diagnostic is not one complete bounded JSON value (%d bytes): %s", len(diagnostic), diagnostic)
	}
	if !strings.Contains(diagnostic, "/packages") || strings.Contains(diagnostic, "PRIVATE_INPUT_VALUE_MUST_NOT_APPEAR") {
		t.Fatalf("diagnostic lost its actionable issue or exposed input content: %s", diagnostic)
	}
}

func TestArgumentContractPreservesTypedEnumConstraints(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []string{"mode"},
		"properties": map[string]any{
			"mode": map[string]any{"type": "string", "enum": []string{"inspect", "execute"}},
		},
	}
	contract := agentRuntimeCompactArgumentContract(schema)
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"inspect", "execute"} {
		if !strings.Contains(string(encoded), value) {
			t.Fatalf("typed enum %q disappeared from feedback: %s", value, encoded)
		}
	}
}

func TestArgumentAdmissionDiagnosticNeverSlicesJSON(t *testing.T) {
	properties := map[string]any{"required_choice": map[string]any{"type": "string"}}
	for index := 0; index < 30; index++ {
		name := string(rune('a'+index)) + strings.Repeat("field", 20)
		properties[name] = map[string]any{"type": "string"}
	}
	schema := agentruntime.ToolSchema{Name: "analysis_input", Parameters: map[string]any{
		"type": "object", "required": []string{"required_choice"}, "properties": properties,
	}}
	gateway := serverAgentRuntimeToolGateway{
		server: &Server{}, toolSchemas: []agentruntime.ToolSchema{schema},
		toolValidators: agentRuntimeToolValidators([]agentruntime.ToolSchema{schema}), hasToolSnapshot: true,
	}
	diagnostic := gateway.ToolCallAdmissionDiagnostic(agentruntime.ToolCall{Name: schema.Name, Arguments: json.RawMessage(`{}`)})
	if !json.Valid([]byte(diagnostic)) || len(diagnostic) > 1800 {
		t.Fatalf("large repair contract became a partial JSON value (%d bytes): %s", len(diagnostic), diagnostic)
	}
	if !strings.Contains(diagnostic, "/required_choice") {
		t.Fatalf("bounded feedback omitted the actual missing field: %s", diagnostic)
	}
}

func TestArgumentFeedbackDoesNotSelectAnAmbiguousAlternative(t *testing.T) {
	schema := agentruntime.ToolSchema{Name: "ambiguous_input", Parameters: map[string]any{
		"type": "object", "oneOf": []any{
			map[string]any{"properties": map[string]any{"mode": map[string]any{"const": "inspect"}}, "required": []string{"left"}},
			map[string]any{"properties": map[string]any{"mode": map[string]any{"const": "inspect"}}, "required": []string{"right"}},
		},
	}}
	validator := compileAgentRuntimeMCPValidator(schema)
	for _, input := range []map[string]any{
		{"mode": "inspect"},
		{"mode": "inspect", "left": true, "right": true},
	} {
		if validator.Validate(input) == nil {
			t.Fatalf("ambiguous alternatives were silently resolved: %#v", input)
		}
	}
	if value := validator.Validate(map[string]any{"mode": "inspect", "left": true}); value != nil {
		t.Fatalf("valid unambiguous full-schema input was rejected: %#v", value)
	}
}

func TestArgumentFeedbackLargeConstraintsRemainCompleteAndImmutable(t *testing.T) {
	diagnostic := map[string]any{
		"code":   "invalid_tool_arguments",
		"issues": []map[string]any{{"path": "/choice", "keyword": "required"}},
		"expectedArguments": map[string]any{"fields": map[string]any{
			"choice": map[string]any{"required": true, "enum": []string{strings.Repeat("choice", 900)}},
		}},
	}
	before, _ := json.Marshal(diagnostic)
	result := boundedAgentRuntimeArgumentDiagnostic(diagnostic)
	after, _ := json.Marshal(diagnostic)
	if !json.Valid([]byte(result)) || len(result) > maxAgentRuntimeArgumentDiagnosticBytes || !strings.Contains(result, "/choice") {
		t.Fatalf("oversized constraint broke repair feedback: %s", result)
	}
	if string(before) != string(after) {
		t.Fatal("feedback budgeting mutated the admission diagnostic")
	}
}
