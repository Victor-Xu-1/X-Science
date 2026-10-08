"""Version-only candidate preparation retains byte-bound source provenance."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from scripts.packaging import version_provenance as provenance
from scripts.packaging.product_version import next_version

ROOT = Path(__file__).resolve().parents[2]


class VersionProvenanceTest(unittest.TestCase):
    def setUp(self):
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
            return provenance.plan(ROOT, self.proposed, changed or set(self.proposed))

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

if __name__ == "__main__":
    unittest.main()
