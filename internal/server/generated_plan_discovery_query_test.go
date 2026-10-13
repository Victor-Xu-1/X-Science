package server

import (
	"context"
	"testing"
)

func TestResearchProcessDefaultsOwnedSourceDiscoveryQuery(t *testing.T) {
	app, _, frameID := newGeneratedPlanProcessFixture(t)
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	schema := annotateOwnedAgentRuntimeToolSchema(agentRCSBSearchToolSchema())
	input := map[string]any{"term_mode": "phrase", "limit": float64(1)}
	got := app.generatedPlanResearchQueryInput(frameID, schema.Name, input, schema.Capabilities)
	if got["query"] != "作用机制 原始证据" {
		t.Fatalf("source discovery lost its missing-query default: %#v", got)
	}
	properties := mapValue(schema.Parameters["properties"])
	for key := range got {
		if _, allowed := properties[key]; !allowed {
			t.Fatalf("plan injected an argument outside the native discovery schema: %s", key)
		}
	}
	if input["query"] != nil || got["term_mode"] != input["term_mode"] || got["limit"] != input["limit"] {
		t.Fatalf("plan mutated the model input or changed its other arguments: input=%#v got=%#v", input, got)
	}
	input["query"] = "explicit structure identity"
	explicit := app.generatedPlanResearchQueryInput(frameID, schema.Name, input, schema.Capabilities)
	if explicit["query"] != input["query"] || len(explicit) != len(input) {
		t.Fatalf("plan replaced an explicit discovery query: %#v", explicit)
	}
}

func TestResearchProcessDiscoveryDefaultsAreCapabilityDriven(t *testing.T) {
	app, _, frameID := newGeneratedPlanProcessFixture(t)
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	for _, test := range []struct {
		name         string
		capabilities []string
		wantQuery    bool
	}{
		{"custom discovery", []string{"source-evidence", "source-discovery"}, true},
		{"analysis evidence", []string{"source-evidence", "analysis"}, false},
		{"source transfer", []string{"source-evidence", "source-discovery", "source-download"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := app.generatedPlanResearchQueryInput(frameID, "fixture_tool", map[string]any{}, test.capabilities)
			if test.wantQuery {
				if got["query"] != "作用机制 原始证据" || len(got) != 1 {
					t.Fatalf("capability-driven discovery did not receive only its primary query: %#v", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("non-discovery execution received plan search arguments: %#v", got)
			}
		})
	}
}
