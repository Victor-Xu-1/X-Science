package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
)

func TestAgentRuntimeAdmissionDiagnosticNamesUnexpectedProperties(t *testing.T) {
	for _, test := range []struct {
		name, arguments, corrected, path, actualType string
		properties                              map[string]any
	}{
		{
			name: "top level", arguments: `{"label":"valid","unexpected":"PRIVATE_VALUE"}`,
			corrected: `{"label":"valid"}`, path: "/unexpected", actualType: "string",
			properties: map[string]any{"label": map[string]any{"type": "string"}},
		},
		{
			name: "nested escaped array item", arguments: `{"a~/":[{"b~/":"PRIVATE_VALUE"}]}`,
			corrected: `{"a~/":[{}]}`, path: "/a~0~1/0/b~0~1", actualType: "string",
			properties: map[string]any{"a~/": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
			}}},
		},
		{
			name: "empty property name", arguments: `{"":true}`,
			corrected: `{}`, path: "/", actualType: "boolean", properties: map[string]any{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := agentruntime.ToolSchema{Name: "mcp__fixture__strict_input", Parameters: map[string]any{
				"type": "object", "additionalProperties": false, "properties": test.properties,
			}}
			gateway := serverAgentRuntimeToolGateway{
				server: &Server{}, toolSchemas: []agentruntime.ToolSchema{schema},
				toolValidators: agentRuntimeToolValidators([]agentruntime.ToolSchema{schema}), hasToolSnapshot: true,
			}
			call := agentruntime.ToolCall{Name: schema.Name, Arguments: json.RawMessage(test.arguments)}
			if gateway.AdmitsToolCall(call) {
				t.Fatal("unsupported property passed strict admission")
			}
			diagnostic := gateway.ToolCallAdmissionDiagnostic(call)
			var value struct {
				Code   string           `json:"code"`
				Issues []map[string]any `json:"issues"`
			}
			if err := json.Unmarshal([]byte(diagnostic), &value); err != nil {
				t.Fatal(err)
			}
			if value.Code != "invalid_tool_arguments" || len(value.Issues) != 1 {
				t.Fatalf("diagnostic = %s", diagnostic)
			}
			issue := value.Issues[0]
			if issue["path"] != test.path || issue["keyword"] != "additionalProperties" || issue["actual_type"] != test.actualType {
				t.Fatalf("specific property diagnostic = %#v", issue)
			}
			if issue["expected"] != nil || issue["actual"] != nil || strings.Contains(diagnostic, "PRIVATE_VALUE") {
				t.Fatalf("diagnostic invented a property type or exposed argument data: %s", diagnostic)
			}
			call.Arguments = json.RawMessage(test.corrected)
			if !gateway.AdmitsToolCall(call) || gateway.ToolCallAdmissionDiagnostic(call) != "" {
				t.Fatal("corrected arguments did not pass the same strict contract")
			}
		})
	}
}

func TestAgentRuntimeValidationSafeActualTypeSurvivesSanitization(t *testing.T) {
	schema := agentruntime.ToolSchema{Name: "mcp__fixture__typed_input", Parameters: map[string]any{
		"type": "object", "required": []any{"count"},
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	}}
	validator := compileAgentRuntimeMCPValidator(schema)
	for _, test := range []struct {
		name, actualType string
		input            map[string]any
	}{
		{name: "missing", actualType: "missing", input: map[string]any{}},
		{name: "wrong type", actualType: "string", input: map[string]any{"count": "PRIVATE_VALUE"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := validator.Validate(test.input)
			issues := sanitizedAgentRuntimeValidationIssues(result["issues"])
			if len(issues) != 1 || issues[0]["path"] != "/count" || issues[0]["actual_type"] != test.actualType {
				t.Fatalf("safe actual type lost: %#v", issues)
			}
			encoded, err := json.Marshal(issues)
			if err != nil || strings.Contains(string(encoded), "PRIVATE_VALUE") || issues[0]["actual"] != nil {
				t.Fatalf("unsafe diagnostic: %s (%v)", encoded, err)
			}
		})
	}
}

func TestAgentRuntimeUnexpectedPropertyFeedbackIsBounded(t *testing.T) {
	validator := compileAgentRuntimeMCPValidator(agentruntime.ToolSchema{Name: "mcp__fixture__bounded_input", Parameters: map[string]any{
		"type": "object", "additionalProperties": false,
	}})
	t.Run("many fields", func(t *testing.T) {
		input := map[string]any{}
		for index := 0; index < 20; index++ {
			input[fmt.Sprintf("extra_%02d", index)] = "PRIVATE_VALUE"
		}
		result := validator.Validate(input)
		issues := sanitizedAgentRuntimeValidationIssues(result["issues"])
		if len(issues) != maxAgentRuntimeMCPValidationIssues {
			t.Fatalf("bounded property count = %d: %#v", len(issues), issues)
		}
		for index, issue := range issues {
			if issue["path"] != fmt.Sprintf("/extra_%02d", index) || issue["actual_type"] != "string" {
				t.Fatalf("property ordering/type = %#v", issues)
			}
		}
		diagnostic := boundedAgentRuntimeArgumentDiagnostic(map[string]any{"code": result["code"], "issues": issues})
		if !json.Valid([]byte(diagnostic)) || len(diagnostic) > maxAgentRuntimeArgumentDiagnosticBytes || strings.Contains(diagnostic, "PRIVATE_VALUE") {
			t.Fatalf("unbounded or unsafe feedback: %s", diagnostic)
		}
	})
	t.Run("long field", func(t *testing.T) {
		name := strings.Repeat("~/界", 1024)
		result := validator.Validate(map[string]any{name: "PRIVATE_VALUE"})
		issues := sanitizedAgentRuntimeValidationIssues(result["issues"])
		if len(issues) != 1 || issues[0]["path_omitted"] != true || issues[0]["path"] != "" || issues[0]["actual_type"] != "string" {
			t.Fatalf("oversized pointer must be explicitly omitted, not fabricated: %#v", issues)
		}
		encoded, _ := json.Marshal(issues)
		if len(encoded) > 512 || strings.Contains(string(encoded), "PRIVATE_VALUE") || strings.Contains(string(encoded), name) {
			t.Fatalf("oversized property metadata escaped its budget: %s", encoded)
		}
	})
}

func TestAgentRuntimeUnexpectedPropertyFeedbackKeepsRequiredRepairPriority(t *testing.T) {
	validator := compileAgentRuntimeMCPValidator(agentruntime.ToolSchema{Name: "mcp__fixture__required_input", Parameters: map[string]any{
		"type": "object", "properties": map[string]any{"options": map[string]any{
			"type": "object", "additionalProperties": false, "required": []any{"choice"},
			"properties": map[string]any{"choice": map[string]any{"type": "string"}},
		}},
	}})
	input := map[string]any{"options": map[string]any{"unexpected": "PRIVATE_VALUE"}}
	result := validator.Validate(input)
	issues := sanitizedAgentRuntimeValidationIssues(result["issues"])
	if len(issues) != 1 || issues[0]["path"] != "/options/choice" || issues[0]["keyword"] != "required" || issues[0]["actual_type"] != "missing" {
		t.Fatalf("required-field repair priority changed: %#v", issues)
	}
	input["options"].(map[string]any)["choice"] = "valid"
	result = validator.Validate(input)
	issues = sanitizedAgentRuntimeValidationIssues(result["issues"])
	if len(issues) != 1 || issues[0]["path"] != "/options/unexpected" || issues[0]["keyword"] != "additionalProperties" {
		t.Fatalf("remaining forbidden field was silently admitted or hidden: %#v", issues)
	}
}
