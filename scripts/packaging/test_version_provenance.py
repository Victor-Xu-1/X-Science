"""Version-only candidate preparation retains byte-bound source provenance."""

import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import py_compile
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

from scripts.packaging import version_provenance as provenance
from scripts.quality.product_version import next_version

ROOT = Path(__file__).resolve().parents[2]


class ReviewedAuditHelperTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="reviewed-audit-helper-")
        self.addCleanup(self.temporary.cleanup)
        self.tooling = Path(self.temporary.name) / "tooling"
        self.candidate = Path(self.temporary.name) / "candidate"
        self.audit = (
            'from frontend_source_namespace import loaded_from\n'
            + ''.join(f'APPROVED_{kind}_FINGERPRINT = "' + "a" * 64 + '"\n' for kind in ("ADAPTATION", "ADDITION", "REMOVAL"))
            + 'def main(arguments):\n    return 0\n'
        )
        for root in (self.tooling, self.candidate):
            for relative, body in ((provenance.AUDIT, self.audit),
                                   (provenance.AUDIT_HELPER, 'loaded_from = __file__\n')):
                target = root / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(body)
        self.package_file = self.tooling / "scripts/packaging/version_provenance.py"
        self.package_file.parent.mkdir(parents=True)
        self.package_file.write_text("# reviewed tooling fixture\n")

    def load(self):
        with patch.object(provenance, "__file__", str(self.package_file)):
            return provenance.reviewed_audit(self.candidate)

    def test_loads_exact_trusted_helper_and_restores_preexisting_cache(self):
        cached = types.ModuleType("frontend_source_namespace")
        cached.loaded_from = "unreviewed cached code"
        with patch.dict(sys.modules, {"frontend_source_namespace": cached}):
            audit = self.load()
            self.assertEqual(audit.loaded_from, str(self.tooling / provenance.AUDIT_HELPER))
            self.assertIs(sys.modules["frontend_source_namespace"], cached)

    def test_rejects_changed_candidate_helper_without_executing_it(self):
        marker = Path(self.temporary.name) / "must-not-execute"
        helper = self.candidate / provenance.AUDIT_HELPER
        helper.write_text(f'from pathlib import Path\nPath({str(marker)!r}).touch()\n')
        with self.assertRaisesRegex(ValueError, "helper requires a reviewed tooling update"):
            self.load()
        self.assertFalse(marker.exists())

    def test_stale_bytecode_cannot_override_verified_helper_source(self):
        helper = self.tooling / provenance.AUDIT_HELPER
        raw = helper.read_bytes()
        stale = b'loaded_from = "BAD"\n'
        helper.write_bytes(stale + b" " * (len(raw) - len(stale)))
        os.utime(helper, (1700000000, 1700000000))
        py_compile.compile(str(helper), doraise=True)
        helper.write_bytes(raw)
        os.utime(helper, (1700000000, 1700000000))
        self.assertEqual(self.load().loaded_from, str(helper))

    def test_symlink_hardlink_parent_alias_and_traversal_are_rejected(self):
        helper = self.candidate / provenance.AUDIT_HELPER
        original = helper.read_bytes()
        for make in (lambda: helper.symlink_to(self.tooling / provenance.AUDIT_HELPER),
                     lambda: os.link(self.tooling / provenance.AUDIT_HELPER, helper)):
            helper.unlink()
            make()
            with self.assertRaisesRegex(ValueError, "reviewed tooling"):
                self.load()
            helper.unlink()
            helper.write_bytes(original)
        for value in ("../audit.py", "/outside.py", "scripts/../audit.py", "scripts\\audit.py"):
            with self.assertRaisesRegex(ValueError, "exact and contained"):
                provenance.regular_audit_input(self.candidate, value)
        with tempfile.TemporaryDirectory() as directory:
            alias = Path(directory) / "alias"
            alias.symlink_to(self.candidate / "scripts/audit", target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "without symlinks"):
                provenance.regular_audit_input(Path(directory), "alias/frontend_source_namespace.py")

    def test_failed_import_restores_cache_and_never_leaves_helper_authority(self):
        for root in (self.tooling, self.candidate):
            (root / provenance.AUDIT).write_text(self.audit + 'raise RuntimeError("fixture import failure")\n')
        cached = types.ModuleType("frontend_source_namespace")
        with patch.dict(sys.modules, {"frontend_source_namespace": cached}):
            with self.assertRaisesRegex(RuntimeError, "fixture import failure"):
                self.load()
            self.assertIs(sys.modules["frontend_source_namespace"], cached)
        previous = sys.modules.pop("frontend_source_namespace", None)
        try:
            with self.assertRaises(RuntimeError):
                self.load()
            self.assertNotIn("frontend_source_namespace", sys.modules)
        finally:
            if previous is not None:
                sys.modules["frontend_source_namespace"] = previous

    def test_oversized_or_nonregular_helper_fails_before_execution(self):
        helper = self.candidate / provenance.AUDIT_HELPER
        helper.write_bytes(b"#" * (4 * 1024 * 1024 + 1))
        with self.assertRaisesRegex(ValueError, "bounded regular"):
            self.load()
        helper.unlink()
        helper.mkdir()
        with self.assertRaisesRegex(ValueError, "bounded regular"):
            self.load()


class VersionProvenanceTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="version-provenance-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "candidate"
        subprocess.run(["git", "clone", "--quiet", "--shared", "--no-hardlinks", str(ROOT), str(self.root)], check=True)
        self.old = provenance.document((ROOT / provenance.IDENTITY).read_bytes())["version"]
        self.new = next_version(self.old)
        self.proposed = {}
        for path, pointers in provenance.projections(ROOT).items():
            value = provenance.document((ROOT / path).read_bytes())
            for pointer in pointers:
                provenance.replace_pointer(value, pointer, self.old, self.new)
            self.proposed[path] = (json.dumps(value, indent=2) + "\n").encode()

    def plan(self, changed=None):
        with contextlib.redirect_stdout(io.StringIO()):
            return provenance.plan(self.root, self.proposed, changed or set(self.proposed))

    def test_tooling_checkout_cannot_trust_itself_as_candidate(self):
        with self.assertRaisesRegex(ValueError, "separate reviewed tooling"):
            provenance.plan(ROOT, self.proposed, set(self.proposed))

    def test_candidate_cannot_alias_the_trusted_auditor(self):
        target = self.root / provenance.AUDIT
        target.unlink()
        target.symlink_to(ROOT / provenance.AUDIT)
        with self.assertRaisesRegex(ValueError, "separate reviewed tooling"):
            self.plan()

    def test_version_only_change_passes_real_audit_and_is_idempotent(self):
        outputs = self.plan()
        self.assertEqual(set(outputs), provenance.DERIVED)
        with tempfile.TemporaryDirectory() as directory:
            candidate = Path(directory) / "candidate"
            subprocess.run(["git", "clone", "--quiet", "--shared", "--no-hardlinks", str(ROOT), str(candidate)], check=True)
            for path, raw in self.proposed.items():
                (candidate / path).write_bytes(raw)
            before = subprocess.run(["python3", "-B", str(candidate / provenance.AUDIT), "--check"], capture_output=True)
            self.assertNotEqual(before.returncode, 0)
            self.assertIn(b"fingerprint mismatch", before.stderr)
            for path, raw in outputs.items():
                (candidate / path).write_bytes(raw)
            after = subprocess.run(["python3", "-B", str(candidate / provenance.AUDIT), "--check"], capture_output=True)
            self.assertEqual(after.returncode, 0, after.stderr.decode())
        self.proposed.update(outputs)
        self.assertEqual(self.plan(), outputs)

    def test_rejects_dependency_or_script_changes(self):
        for field in ("dependencies", "scripts"):
            with self.subTest(field=field):
                original = self.proposed["frontend/package.json"]
                value = provenance.document(original)
                value[field]["unreviewed"] = "not-a-version-change"
                self.proposed["frontend/package.json"] = json.dumps(value).encode()
                with self.assertRaisesRegex(ValueError, "non-version JSON"):
                    self.plan()
                self.proposed["frontend/package.json"] = original

    def test_rejects_other_source_changes(self):
        with self.assertRaisesRegex(ValueError, "non-version changes"):
            self.plan(set(self.proposed) | {"internal/server/server.go"})

    def test_rejects_unaligned_projection_and_version_jumps(self):
        self.proposed["frontend/packages/desktop/package.json"] = (ROOT / "frontend/packages/desktop/package.json").read_bytes()
        with self.assertRaisesRegex(ValueError, "non-version JSON"):
            self.plan()
        for version in (self.old, "0.2.0", "1.0.0"):
            with self.subTest(version=version):
                self.proposed[provenance.IDENTITY] = json.dumps({"version": version}).encode()
                with self.assertRaisesRegex(ValueError, "one counter step"):
                    self.plan()

    def test_duplicate_json_fields_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "duplicate"):
            provenance.document(b'{"version":"0.1.1","version":"0.1.2"}')

    def test_version_preparation_updates_notice_binding_without_changing_upstream_terms(self):
        path = 'docs/licenses/frontend-bundle/NOTICE.txt'
        old_notice = (self.root / path).read_bytes()
        previous = hashlib.sha256((self.root / 'frontend/package-lock.json').read_bytes()).hexdigest()
        current = hashlib.sha256(self.proposed['frontend/package-lock.json']).hexdigest()
        expected = old_notice.replace(previous.encode(), current.encode())
        self.assertEqual(self.plan().get(path), expected)

    def test_stale_notice_is_not_approved_by_version_preparation(self):
        path = self.root / 'docs/licenses/frontend-bundle/NOTICE.txt'
        path.write_text(path.read_text().replace('Package lock SHA-256: ', 'Stale lock SHA-256: '))
        with self.assertRaisesRegex(ValueError, 'notice.*bound'):
            self.plan()

    def test_duplicate_notice_binding_is_rejected(self):
        path = self.root / 'docs/licenses/frontend-bundle/NOTICE.txt'
        binding = next(line for line in path.read_text().splitlines() if line.startswith('Package lock SHA-256: '))
        path.write_text(path.read_text() + '\n' + binding + '\n')
        with self.assertRaisesRegex(ValueError, 'notice.*bound'):
            self.plan()

if __name__ == "__main__":
    unittest.main()
