package server

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKernelSkillPublishSDKForwardsOnlyExplicitRevision(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("targeted SDK serialization test requires existing python3: ", err)
	}
	source, err := filepath.Abs(filepath.Join("..", "..", "assets", "optional", "kernels", "x_science_host_bridge.py"))
	if err != nil {
		t.Fatal(err)
	}
	// Compile only the actual accessor AST, with a controlled protocol endpoint.
	// This never starts a kernel, provider, environment or scientific task.
	script := `import ast, pathlib, sys
source = pathlib.Path(sys.argv[1])
tree = ast.parse(source.read_text(encoding="utf-8"))
accessor = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == "_SkillsAccessor")
calls = []
def rpc(method, args, kwargs):
    calls.append((method, args, kwargs))
    return {"status": "observed"}
scope = {"_host_rpc": rpc}
exec(compile(ast.Module(body=[accessor], type_ignores=[]), str(source), "exec"), scope)
skills = scope["_SkillsAccessor"]()
revision = "a" * 64
skills.publish("fixture", True, revision)
skills.publish("fixture", expected_draft_revision=revision)
skills.publish("fixture")
assert calls == [
    ("host.skills.publish", ["fixture", True], {"expected_draft_revision": revision}),
    ("host.skills.publish", ["fixture", False], {"expected_draft_revision": revision}),
    ("host.skills.publish", ["fixture", False], {"expected_draft_revision": None}),
], calls
try:
    skills.publish("fixture", expected_draft_revision=42)
except TypeError:
    pass
else:
    raise AssertionError("non-string revision accepted")
assert len(calls) == 3, "SDK read/refreshed or invented a publication revision"
print("explicit publication revision serialization passed")
`
	if output, err := exec.Command(python, "-B", "-c", script, source).CombinedOutput(); err != nil {
		t.Fatalf("actual SDK serialization: %v\n%s", err, output)
	}
}
