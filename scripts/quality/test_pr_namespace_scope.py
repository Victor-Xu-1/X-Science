"""Real Go AST and local Git proofs, without running product business suites."""
from pathlib import Path
import json
import subprocess
import tempfile
import unittest

from scripts.quality import pr_dependency_scope as dependency
from scripts.quality import pr_fast_scope as scope
from scripts.quality import pr_namespace_scope as namespace


def git(root, *arguments):
    return subprocess.check_output(["git", "-C", str(root), "-c", "user.name=namespace-proof",
        "-c", "user.email=namespace@example.invalid", *arguments], text=True).strip()


class ASTProofTests(unittest.TestCase):
    def test_scientific_synonym_paths_are_not_product_marks(self):
        self.assertEqual(namespace.canonical_path("internal/synonyms/parser.go"), "internal/synonyms/parser.go")
        self.assertEqual(namespace.canonical_path("internal/Synonyms.go"), "internal/Synonyms.go")
        self.assertEqual(namespace.canonical_path("scripts/dev/synonyms.py"), "scripts/dev/synonyms.py")
        self.assertEqual(namespace.canonical_path("cmd/synonyms/main.go"), "cmd/synonyms/main.go")

    def test_only_imports_identifiers_and_matching_product_prose_are_normalized(self):
        before = 'package sample\nimport "synon-go/core"\n// Synon component\nfunc SynonValue() int { return core.SynonValue() }\n'
        after = 'package sample\nimport "x-science/core"\n// XScience component\nfunc XScienceValue() int { return core.XScienceValue() }\n'
        pairs = [(before, after)]
        cases = [
            ('const Value = 1', 'const Value = 2'),
            ('const Display = "Synon"', 'const Display = "XScience"'),
            ('type Data struct { Value string `json:"synon"` }', 'type Data struct { Value string `json:"xscience"` }'),
            ('func Value() int { return 1 }', 'func Value() int { return 2 }'),
            ('func Synonym() int { return 1 }', 'func XScienceym() int { return 1 }'),
            ('import _ "example.org/synon"', 'import _ "example.org/xscience"'),
            ('//go:embed synon.json\nvar Value string', '//go:embed xscience.json\nvar Value string'),
        ]
        pairs.extend(('package sample\n' + old + '\n', 'package sample\n' + new + '\n') for old, new in cases)
        pairs += [
            ('//go:build linux\n\npackage sample\n', '//go:build windows\n\npackage sample\n'),
            ('//go:build linux\n\npackage sample\n', '\n//go:build linux\n\npackage sample\n'),
            ('package sample\nimport "C"\nfunc Synon() {}\n', 'package sample\nimport "C"\nfunc XScience() {}\n'),
            ('package sample\nfunc Broken(\n', 'package sample\nfunc Broken(\n'),
            ('package sample\nfunc Value() int { SynonLimit := 1; { XScienceLimit := 2; _ = XScienceLimit; return SynonLimit } }\n',
             'package sample\nfunc Value() int { XScienceLimit := 1; { XScienceLimit := 2; _ = XScienceLimit; return XScienceLimit } }\n'),
        ]
        self.assertEqual(namespace.equivalent_sources(pairs), [True] + [False] * (len(pairs) - 1))


class GitNamespaceScopeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-namespace-fixture-")
        self.addCleanup(self.directory.cleanup)
        self.repo = Path(self.directory.name)
        self.write("go.mod", "module synon-go\n\ngo 1.22\n")
        self.write("core/core.go", "package core\nfunc SynonValue() int { return 1 }\n")
        self.write("app/app.go", 'package app\nimport "synon-go/core"\nfunc Value() int { return core.SynonValue() }\n')
        self.write("unrelated/lib.go", "package unrelated\nconst Value = 7\n")
        git(self.repo, "init", "-q", "-b", "main")
        self.base = self.commit("original namespace")

    def write(self, path, text):
        output = self.repo / path
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(text)

    def commit(self, title):
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-qm", title)
        return git(self.repo, "rev-parse", "HEAD")

    def rename(self):
        self.write("go.mod", "module x-science\n\ngo 1.22\n")
        self.write("core/core.go", "package core\nfunc XScienceValue() int { return 1 }\n")
        self.write("app/app.go", 'package app\nimport "x-science/core"\nfunc Value() int { return core.XScienceValue() }\n')

    def test_registered_module_and_namespace_only_sources_do_not_expand_business_scope(self):
        self.rename()
        head = self.commit("canonical namespace")
        self.assertEqual(dependency.changed_modules(self.repo, self.base, head), set())
        self.assertEqual(scope.selected_packages(self.repo, self.base, head)[0], [])

    def test_real_behavior_keeps_production_and_test_reverse_consumers(self):
        self.write("integration/check_test.go", 'package integration\nimport _ "synon-go/core"\n')
        base = self.commit("test consumer")
        self.rename()
        self.write("integration/check_test.go", 'package integration\nimport _ "x-science/core"\n')
        self.write("core/core.go", "package core\nfunc XScienceValue() int { return 2 }\n")
        head = self.commit("real behavior change")
        self.assertEqual(scope.selected_packages(self.repo, base, head)[0],
                         ["x-science/app", "x-science/core", "x-science/integration"])

    def test_exact_counterpart_can_route_changed_command_logic_without_skipping_it(self):
        self.write("cmd/synon/main.go", 'package main\nfunc main() { println("before") }\n')
        base = self.commit("command")
        self.rename()
        (self.repo / "cmd/synon/main.go").unlink()
        self.write("cmd/x-science/main.go", 'package main\nfunc main() { println("after") }\n')
        head = self.commit("relocated changed command")
        self.assertEqual(scope.selected_packages(self.repo, base, head)[0], ["x-science/cmd/x-science"])

    def declare_runtime_inputs(self, paths, manifests=None):
        group = {"name": "runtime", "paths": paths, "packages": ["x-science/core"]}
        if manifests is not None:
            group["manifests"] = manifests
        self.write("scripts/quality/runtime_input_scope.json", json.dumps({
            "schema": "x-science.runtime-input-scope.v1", "groups": [group],
        }))

    def test_changed_asset_counterpart_retains_current_runtime_consumers(self):
        self.write("assets/synonbiomed/input.json", "before")
        self.declare_runtime_inputs(["assets/x-science/input.json"])
        base = self.commit("original runtime input")
        self.rename()
        (self.repo / "assets/synonbiomed/input.json").unlink()
        self.write("assets/x-science/input.json", "after")
        head = self.commit("relocated changed runtime input")
        self.assertEqual(namespace.filter_paths(self.repo, base, head, ["assets/synonbiomed/input.json"]),
                         ["assets/x-science/input.json"])
        self.assertEqual(scope.selected_packages(self.repo, base, head)[0], ["x-science/app", "x-science/core"])

    def test_unregistered_asset_counterpart_does_not_receive_a_namespace_exemption(self):
        self.write("assets/synonbiomed/input.json", "before")
        base = self.commit("unregistered original input")
        self.rename()
        (self.repo / "assets/synonbiomed/input.json").unlink()
        self.write("assets/x-science/input.json", "after")
        head = self.commit("unregistered current input")
        original = ["assets/synonbiomed/input.json"]
        self.assertEqual(namespace.filter_paths(self.repo, base, head, original), original)
        self.assertEqual(scope.selected_packages(self.repo, base, head)[0],
                         ["x-science/app", "x-science/core", "x-science/unrelated"])

    def test_frontend_test_harness_rename_routes_real_changes_without_exempting_them(self):
        old = "frontend/tests/integration/synonbiomedExample.real.test.ts"
        current = "frontend/tests/integration/xscienceExample.real.test.ts"
        self.write(old, "const value = 1;\n")
        base = self.commit("old harness")
        self.rename()
        (self.repo / old).unlink()
        self.write(current, "const value = 2;\n")
        head = self.commit("changed relocated harness")
        self.assertEqual(namespace.filter_paths(self.repo, base, head, [old, current]), [current])

    def test_content_addressed_runtime_paths_require_identical_locked_packages(self):
        root = "assets/optional/conda-runtimes"
        catalog = root + "/manifest.json"
        self.declare_runtime_inputs([catalog], [{"path": catalog, "root": root, "kind": "conda-catalog"}])
        packages = [{"name": "python", "url": "https://example.invalid/python.conda", "sha256": "a" * 64}]

        def generation(name, identifier, package_rows):
            row = {"name": name, "platform": "linux-x86_64"}
            for field, leaf in [("manifestPath", "manifest.json"), ("explicitPath", "explicit.txt"), ("licensesPath", "licenses.json")]:
                row[field] = name + "/" + identifier + "/" + leaf
                value = {"name": name, "platform": row["platform"], "packages": package_rows} if leaf == "manifest.json" else {"name": name}
                self.write(root + "/" + row[field], json.dumps(value))
            self.write(catalog, json.dumps({"schemaVersion": 2, "runtimes": [row]}))
            return row

        before = generation("synon-biomed-python", "old", packages)
        base = self.commit("original locked generation")
        self.rename()
        for field in ("manifestPath", "explicitPath", "licensesPath"):
            (self.repo / root / before[field]).unlink()
        after = generation("x-science-python", "current", packages)
        head = self.commit("same packages current identity")
        original = root + "/" + before["manifestPath"]
        expected = [root + "/" + after["manifestPath"]]
        self.assertEqual(namespace.filter_paths(self.repo, base, head, [original]), expected)
        generation("x-science-python", "current", [dict(packages[0], sha256="b" * 64)])
        changed = self.commit("different dependency generation")
        self.assertEqual(namespace.filter_paths(self.repo, base, changed, [original]), [original])

    def test_unmatched_deleted_source_and_unknown_asset_keep_conservative_fallback(self):
        self.rename()
        (self.repo / "core/core.go").unlink()
        # Keep current packages valid while deliberately providing no counterpart.
        self.write("app/app.go", "package app\nfunc Value() int { return 1 }\n")
        head = self.commit("unmatched deletion")
        self.assertEqual(scope.selected_packages(self.repo, self.base, head)[0],
                         ["x-science/app", "x-science/unrelated"])
        self.assertEqual(scope.select_packages(self.repo, ["unknown/input.json"], scope.packages(self.repo))[0],
                         ["x-science/app", "x-science/unrelated"])

    def test_toolchain_changes_still_have_conservative_dependency_authority(self):
        self.rename()
        self.write("go.mod", "module x-science\n\ngo 1.23\n")
        head = self.commit("toolchain change")
        self.assertIsNone(dependency.changed_modules(self.repo, self.base, head))

    def test_counterpart_collision_stops_instead_of_choosing_a_convenient_file(self):
        self.write("core/Synon.go", "package core\n")
        self.write("core/XScience.go", "package core\n")
        base = self.commit("ambiguous sources")
        self.rename()
        head = self.commit("ambiguous transition")
        with self.assertRaisesRegex(RuntimeError, "ambiguous"):
            namespace.filter_paths(self.repo, base, head, ["core/core.go"])

    def test_mutable_revisions_and_traversal_cannot_form_namespace_evidence(self):
        with self.assertRaisesRegex(ValueError, "immutable"):
            namespace.filter_paths(self.repo, "main", self.base, ["core/core.go"])
        with self.assertRaisesRegex(ValueError, "outside"):
            namespace.filter_paths(self.repo, self.base, self.base, ["../core.go"])


if __name__ == "__main__":
    unittest.main()
