"""Real Git/Go build-context proofs; these tests never execute a Go test binary."""
from __future__ import annotations

import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from scripts.quality import pr_fast_scope as scope


def git(repo: Path, *args: str) -> str:
    return subprocess.check_output(["git", "-C", str(repo), "-c", "user.name=scope", "-c", "user.email=scope@example.invalid", *args], text=True).strip()


class PlatformScopeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="x-science-platform-scope-")
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.environment = patch.dict(os.environ, {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOWORK": "off"})
        self.environment.start()
        self.addCleanup(self.environment.stop)
        self.write("go.mod", "module example/platformscope\n\ngo 1.22\n")
        self.write("lib/lib.go", "package lib\nfunc Value() int { return 1 }\n")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 1 }\n")
        self.write("lib/platform_windows_test.go", "package lib\n")
        self.write("app/app.go", 'package app\nimport "example/platformscope/lib"\nvar Value = lib.Value()\n')
        git(self.repo, "init", "-q", "-b", "main")
        self.commit("base")
        self.base = git(self.repo, "rev-parse", "HEAD")

    def write(self, relative: str, source: str):
        path = self.repo / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(source, encoding="utf-8")

    def commit(self, message: str):
        git(self.repo, "add", ".")
        git(self.repo, "commit", "-qm", message)
        return git(self.repo, "rev-parse", "HEAD")

    def selected(self):
        head = git(self.repo, "rev-parse", "HEAD")
        paths = scope.changed_paths(self.repo, self.base, head)
        return scope.resolve_packages(self.repo, self.base, head, paths)

    def test_linux_windows_only_production_and_added_test_do_not_select_runtime_closure(self):
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.write("lib/reader_windows_test.go", "package lib\n")
        self.commit("windows-only implementation and new test")
        entries = scope.packages(self.repo)
        ignored = next(entry["IgnoredGoFiles"] for entry in entries if entry["ImportPath"].endswith("/lib"))
        self.assertIn("platform_windows.go", ignored)
        self.assertIn("reader_windows_test.go", ignored)
        selected, reason = self.selected()
        self.assertEqual(selected, [])
        self.assertIn("filename", reason)

    def test_windows_context_retains_production_consumers_and_test_only_owner(self):
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("windows implementation")
        with patch.dict(os.environ, {"GOOS": "windows"}):
            self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.write("lib/reader_windows_test.go", "package lib\n")
        self.commit("new windows test")
        with patch.dict(os.environ, {"GOOS": "windows"}):
            self.assertEqual(self.selected()[0], ["example/platformscope/lib"])

    def test_active_source_and_build_tag_transition_retain_original_coverage(self):
        self.write("lib/lib.go", "package lib\nfunc Value() int { return 2 }\n")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("active and inactive source")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])
        self.write("lib/tagged.go", "//go:build linux\n\npackage lib\nfunc Tagged() {}\n")
        self.base = self.commit("active tagged baseline")
        self.write("lib/tagged.go", "//go:build windows\n\npackage lib\nfunc Tagged() {}\n")
        self.commit("tag change inactive only at head")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_deletions_and_rename_to_active_name_never_skip(self):
        (self.repo / "lib/platform_windows.go").unlink()
        self.commit("delete inactive file conservatively")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.base = self.commit("restore baseline")
        git(self.repo, "mv", "lib/platform_windows.go", "lib/renamed.go")
        self.commit("rename into current target")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_filename_inactive_source_embedded_in_production_is_still_an_input(self):
        self.write("root.go", 'package platformscope\nimport _ "embed"\n//go:embed lib/platform_windows.go\nvar Source string\n')
        self.base = self.commit("embed inactive source")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("embedded source change")
        self.assertEqual(self.selected()[0], ["example/platformscope", "example/platformscope/app", "example/platformscope/lib"])

    def test_test_embeds_and_declared_runtime_or_verification_consumers_are_kept(self):
        self.write("lib/lib_test.go", 'package lib\nimport _ "embed"\n//go:embed platform_windows_test.go\nvar Source string\n')
        self.base = self.commit("test embed")
        self.write("lib/platform_windows_test.go", "package lib\n// changed embedded input\n")
        self.commit("embedded test source change")
        self.assertEqual(self.selected()[0], ["example/platformscope/lib"])
        self.base = git(self.repo, "rev-parse", "HEAD")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        head = self.commit("declared input change")
        entries = scope.packages(self.repo)
        excluded = scope.pr_platform_scope.filename_exclusions(self.repo, self.base, head, ["lib/platform_windows.go"], entries)
        self.assertEqual(excluded, {"lib/platform_windows.go"})
        with patch.object(scope.runtime_input_scope, "load", return_value={"lib/platform_windows.go": {"example/platformscope/lib"}}):
            self.assertEqual(scope.select_packages(self.repo, ["lib/platform_windows.go"], entries, filename_exclusions=excluded)[0], ["example/platformscope/app", "example/platformscope/lib"])
        with patch.object(scope.verification_scope, "load", return_value=[{"name": "source-reader", "paths": ["lib/platform_windows.go"], "packages": ["example/platformscope/lib"]}]):
            self.assertEqual(scope.select_packages(self.repo, ["lib/platform_windows.go"], entries, filename_exclusions=excluded)[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_unknown_inputs_and_module_authority_changes_keep_conservative_scope(self):
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.write("assets/unknown.txt", "runtime input\n")
        self.commit("unknown external input")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])
        self.write("go.mod", "module example/platformscope\n\ngo 1.23\n")
        head = self.commit("module authority change")
        entries = scope.packages(self.repo)
        self.assertEqual(scope.pr_platform_scope.filename_exclusions(self.repo, self.base, head, scope.changed_paths(self.repo, self.base, head), entries), set())
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_dirty_active_declaration_and_unavailable_proof_never_skip(self):
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("windows change")
        with patch.object(scope.pr_platform_scope, "_run", side_effect=ValueError("proof unavailable")):
            self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])
        self.write("lib/lib.go", "package lib\nfunc Value() int { return 3 }\n")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_custom_platform_tag_can_make_the_same_filename_active_at_base(self):
        self.write("lib/platform_windows.go", "//go:build !linux\n\npackage lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("head ignored by expression, not necessarily filename")
        with patch.dict(os.environ, {"GOFLAGS": "-tags=windows"}):
            self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_nonregular_preimage_does_not_gain_exclusions(self):
        source = self.repo / "lib/platform_windows.go"
        source.unlink()
        try:
            source.symlink_to("lib.go")
        except OSError as error:
            if os.name == "nt" and getattr(error, "winerror", None) == 1314:
                self.skipTest("Windows symlink fixture privilege is unavailable")
            raise
        self.commit("symlink is not a regular proof input")
        self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_workspace_context_without_an_exact_single_module_proof_stays_conservative(self):
        self.write("go.work", "go 1.22\n\nuse .\n")
        self.base = self.commit("workspace baseline")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        self.commit("workspace windows source")
        with patch.dict(os.environ, {"GOWORK": str(self.repo / "go.work")}):
            self.assertEqual(self.selected()[0], ["example/platformscope/app", "example/platformscope/lib"])

    def test_raw_active_embed_declaration_change_cannot_be_hidden_by_a_filtered_path_list(self):
        self.write("root.go", 'package platformscope\nimport _ "embed"\n//go:embed lib/platform_windows.go\nvar Source string\n')
        self.base = self.commit("base embed declaration")
        self.write("root.go", 'package platformscope\nvar Source = ""\n')
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        head = self.commit("active declaration and excluded source changed")
        entries = scope.packages(self.repo)
        self.assertEqual(scope.pr_platform_scope.filename_exclusions(self.repo, self.base, head, ["lib/platform_windows.go"], entries), set())
        self.assertEqual(self.selected()[0], ["example/platformscope", "example/platformscope/app", "example/platformscope/lib"])

    def test_clean_different_checkout_cannot_prove_requested_head_embed_membership(self):
        self.write("root.go", 'package platformscope\nimport _ "embed"\n//go:embed lib/platform_windows.go\nvar Source string\n')
        self.base = self.commit("A with active embed")
        self.write("lib/platform_windows.go", "package lib\nfunc PlatformValue() int { return 2 }\n")
        requested = self.commit("B only changes filename-inactive source")
        self.write("root.go", 'package platformscope\nvar Source = ""\n')
        actual = self.commit("C clean checkout removes embed")
        self.assertNotEqual(actual, requested)
        self.assertEqual(git(self.repo, "status", "--porcelain"), "")
        paths = scope.changed_paths(self.repo, self.base, requested)
        entries = scope.packages(self.repo)
        self.assertEqual(scope.pr_platform_scope.filename_exclusions(
            self.repo, self.base, requested, paths, entries), set())
        self.assertEqual(scope.resolve_packages(self.repo, self.base, requested, paths)[0],
                         ["example/platformscope/app", "example/platformscope/lib"])


class PlatformDeclarationTests(unittest.TestCase):
    def test_existing_producer_seal_and_verification_group_include_the_new_helper_and_tests(self):
        from scripts.quality import pr_reuse_inputs
        repo = Path(__file__).resolve().parents[2]
        self.assertIn("scripts/quality/pr_platform_scope.py", pr_reuse_inputs.SOURCE_INPUTS)
        group = next(group for group in scope.verification_scope.load(repo)
                     if group["name"] == "verification-selection")
        self.assertIn("scripts/quality/pr_fast_scope.py", group["paths"])
        self.assertIn("scripts/quality/test_pr_fast_scope.py", group["paths"])
        self.assertIn("scripts/quality/pr_platform_scope.py", group["paths"])
        self.assertIn("scripts/quality/test_pr_platform_scope.py", group["paths"])
        self.assertTrue(any("scripts.quality.test_pr_platform_scope" in check and
                            "scripts.quality.test_pr_fast_scope" in check for check in group["checks"]))
