package harnesscontract

import (
	"runtime"
	"testing"
)

func TestModelToolAllowedRequiresExactCanonicalIdentity(t *testing.T) {
	for _, canonical := range []string{"skill", "bash", "web_search"} {
		if !ModelToolAllowed(canonical) {
			t.Fatalf("canonical tool %q was rejected", canonical)
		}
	}
	for _, alias := range []string{"Skill", "Bash", "WebSearch", "mcp__literature__search", "MCP__literature__search"} {
		if ModelToolAllowed(alias) {
			t.Fatalf("legacy or case-drifted tool %q entered the model surface", alias)
		}
	}
}

func TestRootAndFixedJobToolSurfacesStayClosedAndComplete(t *testing.T) {
	roots := RootModelTools()
	if len(roots) < 28 {
		t.Fatalf("root model tools shrank: %v", roots)
	}
	seen := map[string]bool{}
	for _, name := range roots {
		if name == "" || seen[name] {
			t.Fatalf("root model tool identity is empty or duplicated: %q", name)
		}
		seen[name] = true
		if !ModelToolAllowed(name) {
			t.Fatalf("canonical root tool %q was rejected", name)
		}
		if got := ClassifyToolSurface(name); got != ToolSurfaceModelRoot {
			t.Fatalf("ClassifyToolSurface(%q)=%q, want %s", name, got, ToolSurfaceModelRoot)
		}
	}
	if runtime.GOOS == "windows" {
		if !seen["powershell"] || seen["bash"] || ModelToolAllowed("bash") {
			t.Fatalf("windows root surface drifted: %v", roots)
		}
	} else if !seen["bash"] || seen["powershell"] || ModelToolAllowed("powershell") {
		t.Fatalf("unix root surface drifted: %v", roots)
	}

	fixed := FixedJobModelTools()
	if len(fixed) != 10 {
		t.Fatalf("fixed-job tools = %v", fixed)
	}
	for _, name := range fixed {
		if name == "" || seen[name] {
			t.Fatalf("fixed-job tool identity collided with the root surface: %q", name)
		}
		seen[name] = true
		if !ModelToolAllowed(name) {
			t.Fatalf("canonical fixed-job tool %q was rejected", name)
		}
		if got := ClassifyToolSurface(name); got != ToolSurfaceFixedJob {
			t.Fatalf("ClassifyToolSurface(%q)=%q, want %s", name, got, ToolSurfaceFixedJob)
		}
	}
	for _, required := range []string{
		"submit_output", "verdict", "record_summary", "summarize_conversation",
		"emit_memories", "report_input_files", "select_relevant_inputs", "select_skills",
		"create_work_item", "read_onboarding_attachment",
	} {
		if !seen[required] {
			t.Fatalf("fixed-job surface lost %q: %v", required, fixed)
		}
	}
}

func TestModelToolAllowedAndClassifyRejectNonCanonicalIdentities(t *testing.T) {
	for _, name := range []string{"", " ", "\t", "Skill", "Bash", "WebSearch", "TaskRun", "AskUserQuestion"} {
		if ModelToolAllowed(name) {
			t.Fatalf("non-canonical identity %q entered the model surface", name)
		}
	}
	cases := map[string]ToolSurface{
		"":                        ToolSurfaceCompatibilityAPI,
		"cron_tick":               ToolSurfaceInternalControl,
		"im_config":               ToolSurfaceInternalControl,
		"im_message":              ToolSurfaceInternalControl,
		"approval_remembered_x":   ToolSurfaceInternalControl,
		"artifact_get":            ToolSurfaceInternalControl,
		"pairing_status":          ToolSurfaceInternalControl,
		"runtime_get":             ToolSurfaceInternalControl,
		"session_list":            ToolSurfaceInternalControl,
		"settings_get":            ToolSurfaceInternalControl,
		"task_list":               ToolSurfaceInternalControl,
		"mcp__literature__search": ToolSurfaceDynamicMCP,
		"TaskRun":                 ToolSurfaceCompatibilityAPI,
	}
	for name, want := range cases {
		if got := ClassifyToolSurface(name); got != want {
			t.Fatalf("ClassifyToolSurface(%q)=%q want=%q", name, got, want)
		}
	}
}

func TestClassifyToolSurfaceSeparatesExecutionAuthorities(t *testing.T) {
	tests := map[string]ToolSurface{
		"web_search":              ToolSurfaceModelRoot,
		"submit_output":           ToolSurfaceFixedJob,
		"mcp__literature__search": ToolSurfaceDynamicMCP,
		"session_runner_next":     ToolSurfaceInternalControl,
		"settings_set":            ToolSurfaceInternalControl,
		"WebSearch":               ToolSurfaceCompatibilityAPI,
		"TaskRun":                 ToolSurfaceCompatibilityAPI,
	}
	for name, want := range tests {
		if got := ClassifyToolSurface(name); got != want {
			t.Fatalf("ClassifyToolSurface(%q)=%q want=%q", name, got, want)
		}
	}
}
