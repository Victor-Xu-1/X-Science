"""Real temporary Git snapshots: count the pending PR once, not its internal commits."""

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from scripts.packaging.pr_version_gate import read_version, verify_transition


class PrVersionGateTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.git("init", "--quiet")
        self.git("config", "user.name", "Version fixture")
        self.git("config", "user.email", "version@example.invalid")

    def git(self, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=self.root, text=True, stderr=subprocess.PIPE).strip()

    def commit_version(self, version, message="fixture"):
        (self.root / "product-identity.json").write_text(json.dumps({"version": version}))
        self.git("add", "product-identity.json")
        self.git("commit", "--quiet", "-m", message, "--allow-empty")
        return self.git("rev-parse", "HEAD")

    def test_multiple_commits_and_commit_types_are_one_pr_transition(self):
        base = self.commit_version("0.1.2")
        self.commit_version("0.1.2", "docs: clarify usage")
        self.commit_version("0.1.2", "test: add coverage")
        candidate = self.commit_version("0.1.3", "feat!: update capability")
        result = verify_transition(self.root, base, candidate)
        self.assertEqual(result["product_version"], "0.1.3")
        self.assertEqual(result, verify_transition(self.root, base, candidate))
        self.assertEqual(read_version(self.root, candidate), "0.1.3")

    def test_real_git_carry_boundaries(self):
        for old, new in (("0.1.99", "0.2.0"), ("0.9.99", "1.0.0")):
            with self.subTest(old=old):
                base = self.commit_version(old)
                candidate = self.commit_version(new)
                self.assertEqual(verify_transition(self.root, base, candidate)["result"], "PASS")

    def test_zero_base_requires_explicit_initial_main_and_never_bypasses_pr_transition(self):
        head = self.commit_version("0.1.2")
        with self.assertRaisesRegex(ValueError, "unavailable"):
            verify_transition(self.root, "0" * 40, head)
        result = verify_transition(self.root, "0" * 40, head, initial_main=True)
        self.assertEqual(result["transition"], "initial-main-not-a-PR-merge")
        with self.assertRaisesRegex(ValueError, "explicit zero base"):
            verify_transition(self.root, head, head, initial_main=True)

    def test_repeated_or_skipped_version_is_rejected(self):
        base = self.commit_version("0.1.2")
        for version in ("0.1.2", "0.1.4", "0.2.0"):
            with self.subTest(version=version):
                candidate = self.commit_version(version)
                with self.assertRaisesRegex(ValueError, "one counter step"):
                    verify_transition(self.root, base, candidate)

    def test_a_new_main_invalidates_the_old_candidate_version(self):
        old_main = self.commit_version("0.1.2")
        old_candidate = self.commit_version("0.1.3")
        new_main = self.commit_version("0.1.3", "another PR merged")
        verify_transition(self.root, old_main, old_candidate)
        with self.assertRaisesRegex(ValueError, "current main|one counter step"):
            verify_transition(self.root, new_main, old_candidate)

    def test_non_sha_refs_and_missing_identity_fail_closed(self):
        for revision in ("main", "--output=/tmp/unsafe", "a" * 40 + ":other", "A" * 40):
            with self.subTest(revision=revision), self.assertRaisesRegex(ValueError, "exact Git commit"):
                read_version(self.root, revision)
        with self.assertRaisesRegex(ValueError, "unavailable"):
            read_version(self.root, "a" * 40)

    def test_tree_objects_executable_files_and_oversized_inputs_are_not_authorities(self):
        commit = self.commit_version("0.1.2")
        with self.assertRaisesRegex(ValueError, "Git commit"):
            read_version(self.root, self.git("rev-parse", commit + "^{tree}"))
        self.git("update-index", "--chmod=+x", "product-identity.json")
        self.git("commit", "--quiet", "-m", "invalid executable authority")
        with self.assertRaisesRegex(ValueError, "non-executable"):
            read_version(self.root, self.git("rev-parse", "HEAD"))
        (self.root / "product-identity.json").write_text(json.dumps({"version": "0.1.2", "padding": "x" * 70_000}))
        self.git("add", "product-identity.json")
        self.git("update-index", "--chmod=-x", "product-identity.json")
        self.git("commit", "--quiet", "-m", "invalid oversized authority")
        with self.assertRaisesRegex(ValueError, "size limit"):
            read_version(self.root, self.git("rev-parse", "HEAD"))


if __name__ == "__main__":
    unittest.main()
