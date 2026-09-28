package server

import "testing"

func TestMergeAgentRuntimeToolInputCopiesAndOverrides(t *testing.T) {
	input := map[string]any{"keep": "original", "replace": "old"}
	updates := map[string]any{"replace": "new", "add": true}
	got := mergeAgentRuntimeToolInput(input, updates)
	if len(got) != 3 || got["keep"] != "original" || got["replace"] != "new" || got["add"] != true {
		t.Fatalf("merged input=%#v", got)
	}
	got["keep"] = "changed"
	got["replace"] = "changed"
	if len(input) != 2 || input["keep"] != "original" || input["replace"] != "old" || updates["replace"] != "new" {
		t.Fatalf("source maps mutated: input=%#v updates=%#v", input, updates)
	}
	empty := mergeAgentRuntimeToolInput(nil, nil)
	empty["writable"] = true
	if len(empty) != 1 {
		t.Fatal("empty merge must return a writable map")
	}
}
