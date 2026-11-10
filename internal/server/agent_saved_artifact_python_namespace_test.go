package server

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestValidateAgentSavedPythonDynamicNamespace(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("the targeted Python namespace regression requires python3")
	}
	for _, test := range []struct {
		name, source, code string
		missing            []string
	}{
		{"opaque_module_globals", `provider = "def supplied(): return 1"
exec(provider, globals())
print(supplied())
`, "analysis_indeterminate", nil},
		{"literal_exec_control", "exec(\"supplied = 1\")\nprint(supplied)\n", "analysis_indeterminate", nil},
		{"globals_literal_key", "globals()[\"supplied\"] = 1\nprint(supplied)\n", "analysis_indeterminate", nil},
		{"globals_literal_update", "globals().update({\"supplied\": 1})\nprint(supplied)\n", "analysis_indeterminate", nil},
		{"module_star_import", "from math import *\nprint(sqrt(4))\n", "analysis_indeterminate", nil},
		{"loads_before_namespace_providers", `print(key_name, update_name, sqrt(4))
globals()["key_name"] = 1
globals().update({"update_name": 1})
from math import *
`, "unresolved_global", []string{"key_name", "sqrt", "update_name"}},
		{"namespace_rhs_loads_precede_writes", `globals()["key_name"] = key_name
globals().update({"update_name": update_name})
print(key_name, update_name)
`, "unresolved_global", []string{"key_name", "update_name"}},
		{"closed_namespace_mixed_definite", `globals()["key_name"] = 1
globals().update({"update_name": 1})
print(key_name, update_name, definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"star_import_preserves_prior_definite", `print(definitely_missing)
from math import *
print(sqrt(4))
`, "unresolved_global", []string{"definitely_missing"}},
		{"shadowed_globals_mutations", `globals = lambda: {}
globals()["key_name"] = 1
globals().update({"update_name": 1})
print(key_name, update_name)
`, "unresolved_global", []string{"key_name", "update_name"}},
		{"independent_dictionary_mutations", `namespace = {}
namespace["key_name"] = 1
namespace.update({"update_name": 1})
print(key_name, update_name)
`, "unresolved_global", []string{"key_name", "update_name"}},
		{"uninvoked_function_namespace_mutations", `def create():
    globals()["key_name"] = 1
    globals().update({"update_name": 1})
print(key_name, update_name)
`, "unresolved_global", []string{"key_name", "update_name"}},
		{"empty_namespace_update", "globals().update({})\nprint(definitely_missing)\n", "unresolved_global", []string{"definitely_missing"}},
		{"module_with_globals", `import contextlib, io
provider = "def supplied(): return 1"
with contextlib.redirect_stdout(io.StringIO()):
    exec(compile(provider, "synthetic", "exec"), globals())
print(supplied())
`, "analysis_indeterminate", nil},
		{"mixed_definite_and_unknown", `provider = "def supplied(): return 1"
print(definitely_missing)
exec(provider, globals())
print(supplied())
`, "unresolved_global", []string{"definitely_missing"}},
		{"ordinary_missing", "print(definitely_missing)\n", "unresolved_global", []string{"definitely_missing"}},
		{"syntax_even_with_provider", `provider = "def supplied(): return 1"
exec(provider, globals())
def broken(:
`, "syntax_error", nil},
		{"isolated_dictionary", `provider = "def supplied(): return 1"
exec(provider, {})
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"separate_locals_dictionary", `provider = "def supplied(): return 1"
exec(provider, globals(), {})
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"literal_does_not_supply_name", `exec("other = 1", globals())
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"literal_empty_namespace", `exec("pass", globals())
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"missing_before_provider", `provider = "def supplied(): return 1"
print(definitely_missing)
exec(provider, globals())
`, "unresolved_global", []string{"definitely_missing"}},
		{"unrelated_function", `def unrelated():
    exec("other = 1", {})
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"shadowed_globals", `globals = lambda: {}
provider = "def supplied(): return 1"
exec(provider, globals())
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
		{"shadowed_exec", `exec = lambda *args: None
provider = "def supplied(): return 1"
exec(provider, globals())
print(definitely_missing)
`, "unresolved_global", []string{"definitely_missing"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := validateAgentSavedPythonSource(context.Background(), python, []byte(test.source))
			if err != nil {
				t.Fatalf("validator infrastructure failed: %v", err)
			}
			if result.OK || result.Code != test.code || !reflect.DeepEqual(result.Unresolved, test.missing) &&
				(len(result.Unresolved) != 0 || len(test.missing) != 0) {
				t.Fatalf("name proof classification: got=%#v want code=%q missing=%v", result, test.code, test.missing)
			}
			analysis, classificationErr := classifyAgentSavedPythonResult(result)
			if test.code == "analysis_indeterminate" {
				if classificationErr != nil || analysis != agentSavedPythonIndeterminate {
					t.Fatalf("uncertain names must reach the existing advisory classification: analysis=%v err=%v", analysis, classificationErr)
				}
			} else if !errors.Is(classificationErr, errAgentSavedArtifactPythonInvalid) {
				t.Fatalf("definite or syntax findings must remain content-invalid: %v", classificationErr)
			}
		})
	}
}
