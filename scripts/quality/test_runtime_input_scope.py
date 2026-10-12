"""Runtime assets retain exact consumers and their full import closure."""

import copy
import json
import hashlib
from pathlib import Path
import tempfile
import unittest

from scripts.quality import pr_fast_scope as scope, runtime_input_scope as contract


class RuntimeInputScopeTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="x-science-runtime-scope-")
        self.addCleanup(self.directory.cleanup)
        self.repo = Path(self.directory.name)
        self.path = self.repo / contract.CONTRACT
        self.path.parent.mkdir(parents=True)
        self.value = {"schema": "x-science.runtime-input-scope.v1", "groups": [{
            "name": "runtime", "paths": ["assets/runtime.py"], "packages": ["example/scope/lib"],
        }]}
        self.save()
        files = {
            "go.mod": "module example/scope\n\ngo 1.22\n",
            "lib/lib.go": "package lib\n",
            "app/app.go": 'package app\nimport _ "example/scope/lib"\n',
            "consumer/consumer_test.go": 'package consumer\nimport _ "example/scope/app"\n',
            "unrelated/other.go": "package unrelated\n",
        }
        for name, content in files.items():
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        self.entries = scope.packages(self.repo)
        self.all_packages = sorted(e["ImportPath"] for e in self.entries)

    def save(self):
        self.path.write_text(json.dumps(self.value))

    def selected(self, *paths):
        return scope.select_packages(self.repo, list(paths), self.entries)[0]

    def test_asset_selects_production_and_test_consumers_not_unrelated_packages(self):
        self.assertEqual(self.selected("assets/runtime.py"), [
            "example/scope/app", "example/scope/consumer", "example/scope/lib",
        ])
        self.assertEqual(self.selected("assets/runtime.py", "unrelated/other.go"), self.all_packages)

    def test_exact_skill_prose_owns_real_consumers_not_all_skills(self):
        self.value["groups"][0]["paths"] += ["skills/example/SKILL.md"]
        self.save()
        self.assertEqual(self.selected("skills/example/SKILL.md"), self.selected("assets/runtime.py"))
        self.assertEqual(self.selected("skills/other/SKILL.md"), self.all_packages)

    def test_unknown_asset_missing_owner_and_missing_contract_remain_conservative(self):
        self.assertEqual(self.selected("assets/runtime.py.new"), self.all_packages)
        self.value["groups"][0]["packages"] = ["example/scope/deleted"]
        self.save()
        self.assertEqual(self.selected("assets/runtime.py"), self.all_packages)
        self.path.unlink()
        self.assertEqual(self.selected("assets/runtime.py"), self.all_packages)

    def test_invalid_authorities_and_duplicate_paths_fail_closed(self):
        valid = copy.deepcopy(self.value)
        for path in ["go.mod", "assets/go.mod", "internal/runtime.go", "assets/runtime.go",
                     "assets/../outside", "assets//runtime", "assets/*", "assets/./runtime"]:
            with self.subTest(path=path):
                self.value = copy.deepcopy(valid)
                self.value["groups"][0]["paths"] = [path]
                self.save()
                with self.assertRaises(ValueError):
                    contract.load(self.repo)
        for key, value in [("packages", []), ("packages", ["../escape"]),
                           ("paths", ["assets/runtime.py", "assets/runtime.py"])]:
            self.value = copy.deepcopy(valid)
            self.value["groups"][0][key] = value
            self.save()
            with self.assertRaises(ValueError):
                contract.load(self.repo)

    def test_repository_assets_have_real_package_owners_and_keep_core_consumers(self):
        repo = Path(__file__).resolve().parents[2]
        owners = contract.load(repo)
        entries = scope.packages(repo)
        names = {entry["ImportPath"] for entry in entries}
        for path, packages in owners.items():
            self.assertTrue((repo / path).is_file(), path)
            self.assertLessEqual(packages, names)
        selected, _ = scope.select_packages(repo, list(owners), entries)
        for package in ["x-science/internal/assets", "x-science/internal/kernel",
                        "x-science/internal/kernel/detached", "x-science/internal/server",
                        "x-science/cmd/x-science", "x-science/internal/compat/v11reuse"]:
            self.assertIn(package, selected)
        self.assertLess(len(selected), len(entries))

    def manifest_fixture(self):
        root = self.repo / "assets/component"
        root.mkdir(parents=True)
        raw = b"runtime input\n"
        (root / "worker.py").write_bytes(raw)
        manifest = {"schemaVersion": 1, "files": [{"path": "worker.py", "bytes": len(raw),
                    "sha256": hashlib.sha256(raw).hexdigest()}]}
        path = "assets/component.manifest.json"
        (self.repo / path).write_text(json.dumps(manifest))
        self.value["groups"][0]["manifests"] = [{"path": path, "root": "assets/component", "kind": "asset"}]
        self.save()
        return manifest, self.repo / path

    def test_manifest_expands_exact_declared_files_and_keeps_reverse_consumers(self):
        self.manifest_fixture()
        for path in ["assets/component.manifest.json", "assets/component/worker.py"]:
            self.assertEqual(self.selected(path), self.selected("assets/runtime.py"))
        (self.repo / "assets/component/unregistered.py").write_text("unknown")
        self.assertEqual(self.selected("assets/component/unregistered.py"), self.all_packages)

    def test_invalid_manifest_paths_records_missing_files_and_symlinks_fail_closed(self):
        value, path = self.manifest_fixture()
        good = copy.deepcopy(value)
        invalid = []
        for relative in ["../outside", "/absolute", "worker.py/../outside", "worker.go", "worker*.py"]:
            changed = copy.deepcopy(good)
            changed["files"][0]["path"] = relative
            invalid.append(changed)
        invalid += [dict(good, schemaVersion=True), dict(good, files=good["files"] * 2)]
        changed = copy.deepcopy(good)
        changed["files"][0]["unknown"] = "unsafe"
        invalid.append(changed)
        for value in invalid:
            path.write_text(json.dumps(value))
            with self.assertRaises(ValueError):
                contract.load(self.repo)
        path.write_text(json.dumps(good))
        target = self.repo / "assets/component/worker.py"
        target.unlink()
        with self.assertRaises(ValueError):
            contract.load(self.repo)
        target.symlink_to(self.repo / "lib/lib.go")
        with self.assertRaises(ValueError):
            contract.load(self.repo)

    def test_conda_catalog_registers_only_its_exact_current_locked_files(self):
        root = self.repo / "assets/runtime-locks"
        (root / "runtime/generation").mkdir(parents=True)
        entry = {"manifestPath": "runtime/generation/manifest.json", "explicitPath": "runtime/generation/explicit.txt",
                 "licensesPath": "runtime/generation/licenses.json"}
        for value in entry.values():
            (root / value).write_text("locked input")
        (root / "manifest.json").write_text(json.dumps({"schemaVersion": 2, "runtimes": [entry]}))
        self.value["groups"][0]["manifests"] = [{"path": "assets/runtime-locks/manifest.json",
                                                 "root": "assets/runtime-locks", "kind": "conda-catalog"}]
        self.save()
        self.assertEqual(self.selected("assets/runtime-locks/runtime/generation/explicit.txt"), self.selected("assets/runtime.py"))
        self.assertEqual(self.selected("assets/runtime-locks/unregistered/explicit.txt"), self.all_packages)


if __name__ == "__main__":
    unittest.main()
