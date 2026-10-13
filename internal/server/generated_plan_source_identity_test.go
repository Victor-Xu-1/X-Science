package server

import (
	"reflect"
	"testing"
)

func TestResearchProcessPreservesExplicitSourceIdentityBeforeAnyReadReceipt(t *testing.T) {
	server, store, frameID := newGeneratedPlanProcessFixture(t)
	for _, withContinuation := range []bool{false, true} {
		if withContinuation {
			metadata, _, err := store.GetFrameRuntimeMetadata(frameID)
			if err != nil {
				t.Fatal(err)
			}
			step := mapValue(mapValue(metadata.ContextData["_step_statuses"])["module-1"])
			step["research_continuation"] = map[string]any{
				"required": true, "blocking": true,
				"next_actions": []any{map[string]any{"action": "read_source", "tool": "web_fetch", "url": "https://example.org/old"}},
			}
			if _, err := store.SetFrameRuntimeMetadata(frameID, metadata); err != nil {
				t.Fatal(err)
			}
		}
		for _, test := range []struct {
			tool  string
			input map[string]any
		}{
			{"web_search", map[string]any{"query": "a distinct follow-up", "query_variants": []any{"another language"}}},
			{"patent_search", map[string]any{"operation": "search", "query": "a compound patent family"}},
			{"patent_search", map[string]any{"operation": "lookup", "publication_number": "US223898A"}},
			{"patent_search", map[string]any{"operation": "resolve_download", "publication_number": "US223898A"}},
			{"web_fetch", map[string]any{"url": "https://example.org/new", "limit": 2000}},
			{"fetch_article_fulltext", map[string]any{"doi": "10.1000/new-source"}},
			{"binding_mode_analysis", map[string]any{"pdb_id": "1ABC", "ligand_resname": "LIG"}},
		} {
			got := server.generatedPlanResearchQueryInput(frameID, test.tool, test.input)
			if !reflect.DeepEqual(got, test.input) {
				t.Fatalf("%s continuation=%t rewrote a selected source: got=%#v want=%#v", test.tool, withContinuation, got, test.input)
			}
			gateway := serverAgentRuntimeToolGateway{server: server, sessionID: frameID}
			got = gateway.normalizeAdmittedToolArguments(test.tool, test.input)
			for key, value := range test.input {
				if !reflect.DeepEqual(got[key], value) {
					t.Fatalf("gateway changed %s.%s: %#v", test.tool, key, got)
				}
			}
		}
	}
}
