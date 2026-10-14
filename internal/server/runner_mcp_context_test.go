package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

func TestRuntimeMCPContextRequiresExactNamesTypesAndBoundedRepair(t *testing.T) {
	context := runtimeMCPContext([]agentruntime.ToolSchema{{
		Name: "mcp__chembl__drug_search",
		Parameters: map[string]any{"type": "object", "required": []any{"query"}, "properties": map[string]any{
			"query":     map[string]any{"type": "string"},
			"max_phase": map[string]any{"type": "integer"},
		}},
	}}, nil)
	for _, required := range []string{
		"exact tool name", "numbers are unquoted JSON numbers", "arrays are JSON arrays",
		"invalid_tool_arguments", "do not repeat the same invalid arguments",
	} {
		if !strings.Contains(context, required) {
			t.Fatalf("MCP context missing %q: %s", required, context)
		}
	}
	for _, duplicated := range []string{"max_phase:integer", "query:string!", "description="} {
		if strings.Contains(context, duplicated) {
			t.Fatalf("MCP context duplicated JSON Schema detail %q: %s", duplicated, context)
		}
	}
}

func TestRuntimeMCPContextSummarizesConnectorsAndOnlyAnnotatesAdmittedSkillMethods(t *testing.T) {
	context := runtimeMCPContext([]agentruntime.ToolSchema{
		{Name: "mcp__chembl__drug_search", Description: "already in JSON Schema"},
		{Name: "mcp__pubmed__search", Description: "must not be repeated"},
	}, []skills.Skill{{Name: "chemistry", Tools: []string{"mcp__chembl__drug_search"}}})
	if !strings.Contains(context, "mcp__chembl__drug_search | selectedBySkill=chemistry") {
		t.Fatalf("selected tool annotation missing: %s", context)
	}
	if !strings.Contains(context, "- pubmed") || strings.Contains(context, "mcp__pubmed__search") ||
		strings.Contains(context, "already in JSON Schema") {
		t.Fatalf("admitted tool names or annotations are inconsistent: %s", context)
	}
}

func TestRuntimeMCPContextDefersUnselectedMethodCatalog(t *testing.T) {
	schemas := []agentruntime.ToolSchema{{Name: "web_search"}}
	for index := range 200 {
		schemas = append(schemas, agentruntime.ToolSchema{
			Name:        fmt.Sprintf("mcp__chemistry__method_%03d", index),
			Description: "UNTRUSTED_DESCRIPTION_DO_NOT_INJECT",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"records": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			}},
		})
	}
	schemas = append(schemas, agentruntime.ToolSchema{Name: "mcp__literature__search"})
	before, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	context := runtimeMCPContext(schemas, nil)
	for _, required := range []string{
		"Invocation contract:", "skill", "filter", "host.mcp", "chemistry", "literature",
		"exact tool name", "numbers are unquoted JSON numbers", "arrays are JSON arrays",
		"invalid_tool_arguments", "do not repeat the same invalid arguments",
	} {
		if !strings.Contains(context, required) {
			t.Errorf("MCP discovery notice missing %q", required)
		}
	}
	for _, excluded := range []string{"mcp__chemistry__method_", "mcp__literature__search", "UNTRUSTED_DESCRIPTION_DO_NOT_INJECT", "web_search"} {
		if strings.Contains(context, excluded) {
			t.Errorf("unselected method detail was eagerly injected: %q", excluded)
		}
	}
	if len(context) > 1200 {
		t.Errorf("connector discovery grew with unselected method count: %d bytes", len(context))
	}
	t.Logf("201 admitted methods across two connectors produce %d bytes of discovery context", len(context))
	after, err := json.Marshal(schemas)
	if err != nil || string(before) != string(after) {
		t.Fatal("presentation changed the admitted method schemas")
	}
	for left, right := 0, len(schemas)-1; left < right; left, right = left+1, right-1 {
		schemas[left], schemas[right] = schemas[right], schemas[left]
	}
	if reversed := runtimeMCPContext(schemas, nil); reversed != context {
		t.Fatal("connector presentation depends on schema discovery order")
	}
}

func TestRuntimeMCPContextRetainsOnlyAdmittedSelectedMethods(t *testing.T) {
	schemas := []agentruntime.ToolSchema{
		{Name: "mcp__chemistry__search"}, {Name: "mcp__chemistry__details"},
		{Name: "mcp__literature__search"},
	}
	selected := []skills.Skill{
		{Name: "z-selected", Tools: []string{"mcp__chemistry__search", "mcp__absent__forbidden"}},
		{Name: "a-selected", Tools: []string{"mcp__chemistry__search", "mcp__chemistry__search"}},
	}
	before := append([]agentruntime.ToolSchema(nil), schemas...)
	context := runtimeMCPContext(schemas, selected)
	if !strings.Contains(context, "mcp__chemistry__search | selectedBySkill=a-selected,z-selected") {
		t.Fatalf("admitted selected method lost its exact identity and provenance: %s", context)
	}
	for _, excluded := range []string{"mcp__chemistry__details", "mcp__literature__search", "mcp__absent__forbidden", "absent"} {
		if strings.Contains(context, excluded) {
			t.Errorf("unselected or unauthorized identity entered context: %q", excluded)
		}
	}
	if !reflect.DeepEqual(schemas, before) {
		t.Fatal("selected presentation changed the admitted schema authority")
	}
}

func TestRuntimeMCPContextEmptyWithoutAdmittedMCP(t *testing.T) {
	for _, schemas := range [][]agentruntime.ToolSchema{nil, {{Name: "repl"}, {Name: "web_search"}}} {
		if context := runtimeMCPContext(schemas, []skills.Skill{{Name: "unavailable", Tools: []string{"mcp__absent__method"}}}); context != "" {
			t.Fatalf("skill metadata manufactured a connector without admission: %s", context)
		}
	}
}
