package kernel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReplHostMCPNoArgumentsUsesCatalogHostCall(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp.catalog"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			if call.Method != "mcp.catalog" || len(call.Args) != 0 || len(call.Kwargs) != 0 {
				t.Fatalf("catalog host call = %#v", call)
			}
			return map[string]any{
				"usage": "use exact catalog entries",
				"tools": []any{map[string]any{"server": "custom:chem", "method": "lookup"}},
			}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-catalog", `
import host
catalog = host.mcp()
print(catalog["tools"][0]["server"], catalog["tools"][0]["method"])
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" || strings.TrimSpace(outcome.Response.Stdout) != "custom:chem lookup" {
		t.Fatalf("host.mcp catalog outcome = %#v err=%v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPPortableCatalogAliasesShareOneCatalogSnapshot(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	calls := 0
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp.catalog"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			calls++
			return map[string]any{"tools": []any{
				map[string]any{"server": "bundled:pubchem", "server_name": "PubChem", "method": "compound_lookup", "description": "lookup", "read_only": true},
				map[string]any{"server": "bundled:pubchem", "server_name": "PubChem", "method": "compound_search", "description": "search", "read_only": true},
			}}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-portable-catalog", `
import host
servers = host.mcp.list_servers()
methods = host.mcp.list_methods(servers[0])
print(servers[0], methods[0]["name"], methods[1]["name"])
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || calls != 2 ||
		strings.TrimSpace(outcome.Response.Stdout) != "bundled:pubchem compound_lookup compound_search" {
		t.Fatalf("portable catalog outcome=%#v calls=%d", outcome, calls)
	}
}

func TestReplHostMCPListMethodsWalksFilteredCatalogPages(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	calls := 0
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp.catalog"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			calls++
			if len(call.Kwargs) == 0 {
				return map[string]any{"servers": []any{"bundled:bio"}, "tools": []any{}}, nil
			}
			if call.Kwargs["server"] != "bundled:bio" {
				t.Fatalf("filtered catalog call=%#v", call)
			}
			if fmt.Sprint(call.Kwargs["offset"]) == "0" {
				return map[string]any{
					"tools": []any{
						map[string]any{"server": "bundled:bio", "method": "alpha", "description": "a"},
						map[string]any{"server": "bundled:bio", "method": "beta", "description": "b"},
					},
					"has_more": true, "next_offset": int64(2),
				}, nil
			}
			return map[string]any{
				"tools":    []any{map[string]any{"server": "bundled:bio", "method": "gamma", "description": "g"}},
				"has_more": false,
			}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-paged-catalog", `
import host
servers = host.mcp.list_servers()
methods = host.mcp.list_methods(servers[0])
again = host.mcp.list_methods(servers[0])
print(servers[0], ",".join(item["name"] for item in methods), len(again))
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || calls != 3 ||
		strings.TrimSpace(outcome.Response.Stdout) != "bundled:bio alpha,beta,gamma 3" {
		t.Fatalf("paged portable catalog outcome=%#v calls=%d", outcome, calls)
	}
}

func TestReplHostMCPListMethodsKeepsResolvedCatalogIdentity(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{AllowedMethods: []string{"mcp.catalog"}, Handler: func(_ context.Context, call HostCall) (any, error) {
		if len(call.Kwargs) == 0 {
			return map[string]any{"servers": []any{"bundled:source"}}, nil
		}
		return map[string]any{"server_filter": "bundled:source", "tools": []any{map[string]any{"server": "bundled:source", "method": "get_schema", "parameters": []any{}}}}, nil
	}}
	outcome := executeHostCallCell(t, manager, "catalog-alias", `import host
print([m["name"] for m in host.mcp.list_methods("source")])`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || strings.TrimSpace(outcome.Response.Stdout) != "['get_schema']" {
		t.Fatalf("canonical methods discarded: %+v %v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPListMethodsExposesIterableAndSchemaParameterViews(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp.catalog"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			if len(call.Kwargs) == 0 {
				return map[string]any{"servers": []any{"clinical-trials"}, "tools": []any{}}, nil
			}
			return map[string]any{"tools": []any{map[string]any{
				"server": "clinical-trials", "method": "search_trials",
				"output_schema": map[string]any{"type": "object", "properties": map[string]any{"studies": map[string]any{"type": "array"}}},
				"parameters": []any{
					map[string]any{"name": "condition", "type": "string", "required": false},
					map[string]any{"name": "page_size", "type": "integer", "required": true},
				},
			}}}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-schema-views", `
import host
method = host.mcp.list_methods("clinical-trials")[0]
print([item["name"] for item in method["parameters"]])
print(sorted(method["parameters"]["properties"]))
print(sorted(method["input_schema"]["properties"]), method["input_schema"]["required"])
print(method["output_schema"]["properties"]["studies"]["type"])
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" ||
		strings.TrimSpace(outcome.Response.Stdout) != "['condition', 'page_size']\n['condition', 'page_size']\n['condition', 'page_size'] ['page_size']\narray" {
		t.Fatalf("portable MCP parameter views outcome=%#v err=%v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPListMethodsPreservesFullSchemasAcrossPagesAndLargeResultStream(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	nested := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"query", "choice"},
		"$defs": map[string]any{"Choice": map[string]any{"oneOf": []any{
			map[string]any{"type": "string", "enum": []string{"exact", "approximate"}},
			map[string]any{"type": "integer", "minimum": 1},
		}}},
		"properties": map[string]any{
			"query": map[string]any{"type": "object", "properties": map[string]any{
				"terms": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "required": []string{"terms"}, "additionalProperties": false},
			"choice": map[string]any{"$ref": "#/$defs/Choice"},
			"samples": map[string]any{"anyOf": []any{
				map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Choice"}},
				map[string]any{"type": "null"},
			}},
		},
	}
	large := map[string]any{"type": "object", "properties": map[string]any{
		"note": map[string]any{"type": "string", "description": strings.Repeat("x", maxHostResultBytes+128)},
	}, "required": []string{"note"}, "additionalProperties": false}
	nestedJSON, err := json.Marshal(nested)
	if err != nil {
		t.Fatal(err)
	}
	largeJSON, err := json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	policy := &HostCallPolicy{AllowedMethods: []string{"mcp.catalog"}, Handler: func(_ context.Context, call HostCall) (any, error) {
		calls++
		if call.Method != "mcp.catalog" {
			t.Fatalf("discovery invoked a method: %#v", call)
		}
		if len(call.Kwargs) == 0 {
			return map[string]any{"servers": []string{"custom:source"}}, nil
		}
		if call.Kwargs["server"] != "custom:source" {
			t.Fatalf("server identity changed: %#v", call)
		}
		if fmt.Sprint(call.Kwargs["offset"]) == "0" {
			return map[string]any{"server_filter": "custom:source", "tools": []any{map[string]any{
				"server": "custom:source", "method": "alpha", "input_schema": nested, "input_schema_status": "complete",
				"parameters": []any{map[string]any{"name": "query", "type": "object", "required": true}},
			}}, "has_more": true, "next_offset": 1}, nil
		}
		return map[string]any{"server_filter": "custom:source", "tools": []any{map[string]any{
			"server": "custom:source", "method": "beta", "input_schema": large, "input_schema_status": "complete",
			"parameters": []any{map[string]any{"name": "note", "type": "string", "required": true}},
		}}, "has_more": false}, nil
	}}
	code := fmt.Sprintf(`import host, hashlib, json
servers = host.mcp.list_servers()
methods = host.mcp.list_methods(servers[0])
assert [m["name"] for m in methods] == ["alpha", "beta"]
actual = [hashlib.sha256(json.dumps(m["input_schema"], sort_keys=True, separators=(",", ":")).encode()).hexdigest() for m in methods]
assert actual == ["%x", "%x"], actual
assert [m["input_schema_status"] for m in methods] == ["complete", "complete"]
assert methods[0]["input_schema"]["properties"]["query"]["required"] == ["terms"]
assert methods[0]["input_schema"]["properties"]["choice"]["$ref"] == "#/$defs/Choice"
assert len(methods[1]["input_schema"]["properties"]["note"]["description"]) == %d
assert [p["name"] for p in methods[0]["parameters"]] == ["query"]
assert methods[0]["parameters"]["properties"]["query"]["type"] == "object"
assert host.mcp.list_methods(servers[0]) == methods
print("complete schemas preserved across pages and streamed result")
`, sha256.Sum256(nestedJSON), sha256.Sum256(largeJSON), maxHostResultBytes+128)
	outcome := executeHostCallCell(t, manager, "mcp-complete-input-schemas", code, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" || calls != 3 ||
		strings.TrimSpace(outcome.Response.Stdout) != "complete schemas preserved across pages and streamed result" {
		t.Fatalf("full discovery schemas outcome=%#v calls=%d err=%v", outcome.Response, calls, outcome.Err)
	}
}

func TestReplHostMCPListMethodsLabelsLegacyAndUnavailableInputSchemas(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{AllowedMethods: []string{"mcp.catalog"}, Handler: func(_ context.Context, call HostCall) (any, error) {
		if len(call.Kwargs) == 0 {
			return map[string]any{"servers": []string{"source"}}, nil
		}
		return map[string]any{"server_filter": "source", "tools": []any{
			map[string]any{"server": "source", "method": "legacy", "parameters": []any{map[string]any{"name": "query", "type": "string", "required": true}}},
			map[string]any{"server": "source", "method": "unavailable", "input_schema": nil, "input_schema_status": "unavailable", "parameters": []any{}},
		}}, nil
	}}
	outcome := executeHostCallCell(t, manager, "mcp-legacy-input-schemas", `import host
methods = host.mcp.list_methods("source")
assert methods[0]["input_schema_status"] == "projected_legacy"
assert methods[0]["input_schema"]["required"] == ["query"]
assert methods[1]["input_schema_status"] == "unavailable"
assert methods[1]["input_schema"] is None
print("legacy and unavailable schemas labeled")
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" ||
		strings.TrimSpace(outcome.Response.Stdout) != "legacy and unavailable schemas labeled" {
		t.Fatalf("schema status outcome=%#v err=%v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPListMethodsRejectsInvalidCompleteInputSchema(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{AllowedMethods: []string{"mcp.catalog"}, Handler: func(_ context.Context, call HostCall) (any, error) {
		if len(call.Kwargs) == 0 {
			return map[string]any{"servers": []string{"source"}}, nil
		}
		server := fmt.Sprint(call.Kwargs["server"])
		tool := map[string]any{"server": server, "method": "inspect", "input_schema_status": "complete"}
		switch server {
		case "null":
			tool["input_schema"] = nil
		case "array":
			tool["input_schema"] = []any{}
		case "scalar":
			tool["input_schema"] = true
		case "missing":
		case "invalid-status":
			tool["input_schema"], tool["input_schema_status"] = map[string]any{}, "unknown"
		case "unavailable-with-value":
			tool["input_schema"], tool["input_schema_status"] = map[string]any{}, "unavailable"
		}
		return map[string]any{"server_filter": server, "tools": []any{tool}}, nil
	}}
	outcome := executeHostCallCell(t, manager, "mcp-invalid-input-schemas", `import host
rejected = 0
for server in ["null", "array", "scalar", "missing", "invalid-status", "unavailable-with-value"]:
    try:
        host.mcp.list_methods(server)
    except ValueError:
        rejected += 1
assert rejected == 6, rejected
print("invalid authoritative schemas rejected")
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" ||
		strings.TrimSpace(outcome.Response.Stdout) != "invalid authoritative schemas rejected" {
		t.Fatalf("invalid full schema outcome=%#v err=%v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPMethodSpellingListMethodsUsesCatalogDiscovery(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp.catalog"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			if call.Method != "mcp.catalog" {
				t.Fatalf("list_methods host call=%#v", call)
			}
			if len(call.Kwargs) == 0 {
				return map[string]any{"servers": []any{"pubmed"}, "tools": []any{}}, nil
			}
			if call.Kwargs["server"] != "pubmed" {
				t.Fatalf("filtered list_methods host call=%#v", call)
			}
			return map[string]any{"tools": []any{map[string]any{
				"server": "pubmed", "method": "search_articles", "parameters": []any{},
			}}}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-list-method-spelling", `
import host
print(host.mcp("pubmed", "list_methods")[0]["name"])
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || strings.TrimSpace(outcome.Response.Stdout) != "search_articles" {
		t.Fatalf("portable list_methods spelling outcome=%#v err=%v", outcome.Response, outcome.Err)
	}
}

func TestReplHostMCPCallAliasUsesCanonicalHostBridge(t *testing.T) {
	manager, _ := newHostCallTestSession(t, nil)
	policy := &HostCallPolicy{
		AllowedMethods: []string{"mcp"},
		Handler: func(_ context.Context, call HostCall) (any, error) {
			if call.Method != "mcp" || len(call.Args) != 3 {
				t.Fatalf("MCP host call = %#v", call)
			}
			return map[string]any{"found": false, "status": "not_found"}, nil
		},
	}
	outcome := executeHostCallCell(t, manager, "exec-mcp-call-alias", `
import host
result = host.mcp.call("clinical-trials", "get_trial_details", {"nct_id": "NCT00000000"})
print(result["found"], result["status"])
`, policy)
	if outcome.Err != nil || outcome.Response.Error != "" || outcome.Response.Stderr != "" ||
		strings.TrimSpace(outcome.Response.Stdout) != "False not_found" {
		t.Fatalf("host.mcp.call alias outcome = %#v err=%v", outcome.Response, outcome.Err)
	}
}
