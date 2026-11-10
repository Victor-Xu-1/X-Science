package server

import (
	"context"
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
		})
	}
}
