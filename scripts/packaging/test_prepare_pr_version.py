"""Real clean clones and real provenance; metadata preparation never advances main."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from scripts.packaging import prepare_pr_version as prepare
from scripts.packaging import version_provenance as provenance
from scripts.quality.product_version import next_version

ROOT = Path(__file__).resolve().parents[2]


class PreparePrVersionTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="synon-version-prepare-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "candidate"
        subprocess.run(["git", "clone", "--quiet", "--shared", "--no-hardlinks", str(ROOT), str(self.root)], check=True)
        self.git("config", "user.name", "Version fixture")
        self.git("config", "user.email", "version@example.invalid")
        self.head = self.git("rev-parse", "HEAD")
        self.git("checkout", "--quiet", "-b", "version-preparation-fixture", self.head)
        self.old = json.loads((self.root / "product-identity.json").read_text())["version"]
        self.new = next_version(self.old)

    def git(self, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=self.root, text=True, stderr=subprocess.PIPE).strip()

    def plan(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return prepare.plan(self.root, self.head, self.head)

    def test_plan_apply_and_repeated_preparation_preserve_nonversion_data(self):
        before = {name: (self.root / name).read_bytes() for name in provenance.projections(self.root)}
        planned = self.plan()
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertLessEqual(set(planned.outputs), set(provenance.projections(self.root)) | provenance.DERIVED)
        prepare.apply(self.root, planned)
        for name, pointers in provenance.projections(self.root).items():
            expected = provenance.document(before[name])
            for pointer in pointers:
                provenance.replace_pointer(expected, pointer, self.old, self.new)
            self.assertEqual(provenance.document((self.root / name).read_bytes()), expected)
        checked = subprocess.run(["python3", "-B", str(self.root / provenance.AUDIT), "--check"],
                                 cwd=self.root, capture_output=True, text=True)
        self.assertEqual(checked.returncode, 0, checked.stderr)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.head)
        self.git("add", "--", *sorted(planned.outputs))
        self.git("commit", "--quiet", "-m", "chore: prepare one candidate version")
        prepared_head = self.git("rev-parse", "HEAD")
        with contextlib.redirect_stdout(io.StringIO()):
            repeated = prepare.plan(self.root, self.head, prepared_head)
        self.assertEqual(repeated.outputs, {})
        self.assertEqual(repeated.version, self.new)
        prepare.apply(self.root, repeated)
        self.assertEqual(self.git("status", "--porcelain"), "")
        self.assertEqual(self.git("rev-parse", "HEAD"), prepared_head)

    def test_dirty_worktree_is_not_modified(self):
        (self.root / "README.md").write_text("uncommitted user work\n")
        with self.assertRaisesRegex(ValueError, "clean task"):
            self.plan()
        self.assertEqual((self.root / "README.md").read_text(), "uncommitted user work\n")

    def test_main_and_detached_checkouts_cannot_be_prepared(self):
        self.git("branch", "-m", "main")
        with self.assertRaisesRegex(ValueError, "never main"):
            self.plan()
        self.git("checkout", "--quiet", "--detach")
        with self.assertRaisesRegex(ValueError, "task branch"):
            self.plan()

    def test_changed_head_after_plan_is_not_written(self):
        planned = self.plan()
        self.git("commit", "--quiet", "--allow-empty", "-m", "concurrent head change")
        with self.assertRaisesRegex(ValueError, "head changed"):
            prepare.apply(self.root, planned)
        self.assertEqual(json.loads((self.root / "product-identity.json").read_text())["version"], self.old)

    def test_write_failure_restores_every_attempted_file_and_preserves_modes(self):
        planned = self.plan()
        before = {name: ((self.root / name).read_bytes(), (self.root / name).stat().st_mode)
                  for name in planned.outputs}
        original_write = prepare.atomic_write
        calls = 0
        def interrupted_write(target, raw):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise OSError("injected metadata write failure")
            original_write(target, raw)
        with patch.object(prepare, "atomic_write", side_effect=interrupted_write):
            with self.assertRaisesRegex(OSError, "injected"):
                prepare.apply(self.root, planned)
        for name, (raw, mode) in before.items():
            self.assertEqual((self.root / name).read_bytes(), raw, name)
            self.assertEqual((self.root / name).stat().st_mode, mode, name)
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_derived_symlink_is_rejected_before_any_write(self):
        target = self.root / provenance.LICENSES
        raw = target.read_bytes()
        alternate = self.root / "frontend/alternate-license.json"
        alternate.write_bytes(raw)
        target.unlink()
        target.symlink_to(alternate.name)
        self.git("add", "frontend")
        self.git("commit", "--quiet", "-m", "untrusted derived output link")
        self.head = self.git("rev-parse", "HEAD")
        with self.assertRaisesRegex(ValueError, "regular file"):
            self.plan()
        self.assertEqual(alternate.read_bytes(), raw)
        self.assertEqual(json.loads((self.root / "product-identity.json").read_text())["version"], self.old)

    def test_notice_symlink_is_rejected_before_any_write(self):
        target = self.root / provenance.NOTICES
        alternate = self.root / 'alternate-notice.txt'
        raw = target.read_bytes()
        alternate.write_bytes(raw)
        target.unlink()
        target.symlink_to(alternate)
        self.git('add', provenance.NOTICES, 'alternate-notice.txt')
        self.git('commit', '--quiet', '-m', 'untrusted notice link')
        self.head = self.git('rev-parse', 'HEAD')
        with self.assertRaisesRegex(ValueError, 'regular file'):
            self.plan()
        self.assertEqual(alternate.read_bytes(), raw)

    def test_projection_symlinks_are_not_followed(self):
        target = self.root / "frontend/package.json"
        raw = target.read_bytes()
        target.unlink()
        alternate = self.root / "frontend/alternate.json"
        alternate.write_bytes(raw)
        target.symlink_to("alternate.json")
        self.git("add", "frontend")
        self.git("commit", "--quiet", "-m", "untrusted projection link")
        self.head = self.git("rev-parse", "HEAD")
        with self.assertRaisesRegex(ValueError, "inside the task worktree"):
            self.plan()
        self.assertEqual(alternate.read_bytes(), raw)

    def test_candidate_audit_implementation_is_never_executed(self):
        audit = self.root / provenance.AUDIT
        sentinel = self.root / "untrusted-code-executed"
        audit.write_text(audit.read_text() + f"\nfrom pathlib import Path\nPath({str(sentinel)!r}).write_text('executed')\n")
        self.git("add", provenance.AUDIT)
        self.git("commit", "--quiet", "-m", "untrusted candidate audit")
        self.head = self.git("rev-parse", "HEAD")
        with self.assertRaisesRegex(ValueError, "reviewed tooling update"):
            self.plan()
        self.assertFalse(sentinel.exists())


if __name__ == "__main__":
    unittest.main()
