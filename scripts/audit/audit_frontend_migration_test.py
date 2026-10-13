from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import unittest


SCRIPT = Path(__file__).with_name("audit_frontend_migration.py")
SPEC = importlib.util.spec_from_file_location("audit_frontend_migration", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
# The standalone auditor has a sibling helper. Load it using the same script
# directory as direct execution without leaking search-path changes to tests.
previous_path = sys.path[:]
try:
    sys.path.insert(0, str(SCRIPT.parent))
    SPEC.loader.exec_module(MODULE)
finally:
    sys.path[:] = previous_path


class FrontendMigrationPolicyTests(unittest.TestCase):
    def test_frontend_readme_is_an_exact_documentation_addition(self) -> None:
        self.assertIsNotNone(MODULE.addition_reason("README.md"))
        self.assertIsNone(MODULE.addition_reason("UNREVIEWED.md"))
        self.assertIsNone(MODULE.addition_reason("docs/README.md"))

    def test_text_publication_coverage_is_an_exact_addition_not_a_directory_grant(self) -> None:
        self.assertIsNotNone(MODULE.addition_reason("packages/desktop/src/common/chat/textPublicationCoverage.ts"))
        self.assertIsNone(MODULE.addition_reason("packages/desktop/src/common/chat/unreviewedPublicationCoverage.ts"))

    def test_addition_rules_are_scoped_and_exact_manifest_is_still_supported(self) -> None:
        self.assertIsNotNone(MODULE.addition_reason("package-lock.json"))
        self.assertIsNotNone(
            MODULE.addition_reason("packages/desktop/src/renderer/services/currentAuthority.ts")
        )
        self.assertIsNotNone(MODULE.addition_reason("tests/unit/currentAuthority.test.ts"))
        self.assertIsNone(MODULE.addition_reason("packages/desktop/src/common/unreviewedAuthority.ts"))
        self.assertIsNone(MODULE.addition_reason("unreviewed-root-file.txt"))

    def test_addition_fingerprint_binds_path_and_content_hash(self) -> None:
        baseline = [{"path": "tests/unit/a.test.ts", "sha256": "a" * 64}]
        changed_path = [{"path": "tests/unit/b.test.ts", "sha256": "a" * 64}]
        changed_content = [{"path": "tests/unit/a.test.ts", "sha256": "b" * 64}]
        self.assertNotEqual(MODULE.addition_fingerprint(baseline), MODULE.addition_fingerprint(changed_path))
        self.assertNotEqual(MODULE.addition_fingerprint(baseline), MODULE.addition_fingerprint(changed_content))

    def test_anndata_fixture_additions_are_exact_and_first_party(self) -> None:
        prefix = "tests/fixtures/anndata-categorical/"
        expected = {
            prefix + "README.md",
            prefix + "categorical-template.h5ad",
            prefix + "conform-ordered-bool.py",
            prefix + "create-template.mjs",
        }
        self.assertEqual({path for path in MODULE.ALLOWED_ADDITIONS if path.startswith(prefix)}, expected)
        for path in sorted(expected):
            with self.subTest(path=path):
                reason = MODULE.addition_reason(path)
                self.assertIsNotNone(reason)
                self.assertIn("first-party synthetic", reason)
                self.assertIn("scaffold", reason)

    def test_anndata_fixture_additions_do_not_grant_a_directory(self) -> None:
        for path in (
            "tests/fixtures/anndata-categorical/unknown.txt",
            "tests/fixtures/anndata-categorical/README.md/unknown.txt",
            "tests/fixtures/anndata-categorical-extra/README.md",
            "tests/fixtures/anndata-categorical/README.md.bak",
            "tests/fixtures/anndata-categorical/CATEGORICAL-TEMPLATE.h5ad",
        ):
            with self.subTest(path=path):
                self.assertIsNone(MODULE.addition_reason(path))

    def test_anndata_fixture_additions_retain_required_body_checks(self) -> None:
        # These tiny files test provenance policy, not HDF5 or scientific results.
        # Actual fixture parsing is covered by XScienceHdf5.worker.test.ts.
        import tempfile
        from unittest.mock import patch

        allowed = {
            path: reason for path, reason in MODULE.ALLOWED_ADDITIONS.items()
            if path.startswith("tests/fixtures/anndata-categorical/")
        }
        with tempfile.TemporaryDirectory() as temporary:
            frontend = Path(temporary)
            bodies = {path: ("synthetic policy fixture: " + path + "\n").encode() for path in allowed}
            for path, body in bodies.items():
                target = frontend / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(body)
            records = [
                {"path": path, "sha256": MODULE.sha256_file(frontend / path)}
                for path in sorted(allowed)
            ]
            with patch.multiple(
                MODULE,
                ALLOWED_ADAPTATIONS={}, ALLOWED_ADAPTATION_RULES=(),
                ALLOWED_ADDITIONS=allowed, ALLOWED_ADDITION_RULES=(),
                ALLOWED_REMOVALS={}, ALLOWED_REMOVAL_RULES=(),
                APPROVED_ADAPTATION_FINGERPRINT=MODULE.adaptation_fingerprint([]),
                APPROVED_ADDITION_FINGERPRINT=MODULE.addition_fingerprint(records),
                APPROVED_REMOVAL_FINGERPRINT=MODULE.removal_fingerprint([]),
            ):
                source = {"files": []}
                (frontend / MODULE.SOURCE_MANIFEST).write_text('{"files": []}\n', encoding="utf-8")
                manifest = MODULE.migration_manifest(frontend, source)
                self.assertEqual([record["path"] for record in manifest["additions"]], sorted(allowed))
                for path, body in bodies.items():
                    target = frontend / path
                    with self.subTest(path=path, change="body"):
                        target.write_bytes(body + b"changed\n")
                        with self.assertRaisesRegex(RuntimeError, "addition fingerprint mismatch"):
                            MODULE.migration_manifest(frontend, source)
                        target.write_bytes(body)
                    with self.subTest(path=path, change="missing"):
                        target.unlink()
                        with self.assertRaisesRegex(RuntimeError, "required migrated frontend additions are missing"):
                            MODULE.migration_manifest(frontend, source)
                        target.write_bytes(body)
                rogue = frontend / "tests/fixtures/anndata-categorical/unknown.txt"
                rogue.write_bytes(b"unreviewed fifth file\n")
                with self.assertRaisesRegex(RuntimeError, "unclassified migrated frontend additions"):
                    MODULE.migration_manifest(frontend, source)


if __name__ == "__main__":
    unittest.main()
