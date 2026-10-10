package capabilities

import (
	"reflect"
	"testing"
)

func TestMergeUniqueAndPrefixedStepsPreserveCapabilityActions(t *testing.T) {
	merged := mergeUnique([]string{"list_tools", "web_search", ""}, []string{"web_search", "web_fetch", ""})
	want := []string{"list_tools", "web_search", "web_fetch"}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("mergeUnique=%v want=%v", merged, want)
	}
	steps := prefixedSteps("Tools", []string{"web_search", "", "web_fetch"})
	if !reflect.DeepEqual(steps, []string{"Tools:web_search", "Tools:web_fetch"}) {
		t.Fatalf("prefixedSteps=%v", steps)
	}
}

func TestCapabilityReportLookupsStayExact(t *testing.T) {
	report := CompactSynon()
	if !report.ContainsModule("service-operations") || report.ContainsModule("Service-Operations") {
		t.Fatal("module lookup drifted from exact retained IDs")
	}
	if !report.ContainsWorkflow("tool-execution-contracts") || report.ContainsWorkflow("tool_execution_contracts") {
		t.Fatal("workflow lookup drifted from exact retained IDs")
	}
	if !report.ContainsLabel("Tool Registry") || report.ContainsLabel("tool registry") {
		t.Fatal("label lookup drifted from exact retained labels")
	}
	if !report.ContainsAction("web_search") || report.ContainsAction("WebSearch") {
		t.Fatal("action lookup advertised a retired alias or lost the canonical tool")
	}
}
