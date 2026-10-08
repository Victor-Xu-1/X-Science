"""Real Git metadata filtering retains implementation/dependency changes."""

import contextlib
import io
import json
import re
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.packaging import prepare_pr_version as prepare
from scripts.quality import pr_fast_scope as scope, pr_metadata_scope as metadata

ROOT = Path(__file__).resolve().parents[2]


class MetadataScopeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory(prefix="pr-metadata-scope-")
        cls.repo = Path(cls.directory.name) / "candidate"
        subprocess.run(["git", "clone", "--quiet", "--shared", "--no-hardlinks", str(ROOT), str(cls.repo)], check=True)
        cls.git("config", "user.name", "Metadata fixture")
        cls.git("config", "user.email", "metadata@example.invalid")
        cls.git("checkout", "-qb", "metadata-fixture")
        cls.base = cls.git("rev-parse", "HEAD")
        with contextlib.redirect_stdout(io.StringIO()):
            prepared = prepare.plan(cls.repo, cls.base, cls.base)
        prepare.apply(cls.repo, prepared)
        cls.git("add", "--", *prepared.outputs)
        cls.git("commit", "-qm", "one version increment")
        cls.head = cls.git("rev-parse", "HEAD")

    @classmethod
    def tearDownClass(cls):
        cls.directory.cleanup()

    @classmethod
    def git(cls, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=cls.repo, text=True).strip()

    def test_proved_version_only_paths_do_not_require_runtime_or_frontend_suites(self):
        paths = scope.changed_paths(self.repo, self.base, self.head)
        filtered, ignored = metadata.filter_paths(self.repo, self.base, self.head, paths)
        self.assertEqual(filtered, [])
        self.assertEqual(set(ignored), set(paths))
        self.assertEqual(len(ignored), 7)

    def test_frontend_metadata_still_runs_fresh_provenance_but_not_npm(self):
        argv = ['pr_fast_scope.py', '--repo', str(self.repo), '--base', self.base, '--head', self.head, '--frontend']
        with patch.object(sys, 'argv', argv), patch.object(
            scope, 'run', return_value=subprocess.CompletedProcess([], 0),
        ) as invoke:
            self.assertEqual(scope.main(), 0)
        self.assertEqual(invoke.call_count, 1)
        self.assertIn('audit_frontend_migration.py', invoke.call_args.args[2])

    def test_failed_fresh_provenance_does_not_pass_metadata_scope(self):
        argv = ['pr_fast_scope.py', '--repo', str(self.repo), '--base', self.base, '--head', self.head, '--frontend']
        with patch.object(sys, 'argv', argv), patch.object(
            scope, 'run', return_value=subprocess.CompletedProcess([], 19),
        ):
            self.assertEqual(scope.main(), 19)

    def test_real_bad_pin_cannot_hide_behind_version_only_selection(self):
        with tempfile.TemporaryDirectory(prefix="metadata-invalid-pin-") as directory:
            candidate = Path(directory) / "candidate"
            subprocess.run(["git", "clone", "--quiet", "--shared", "--no-hardlinks",
                            str(self.repo), str(candidate)], check=True)
            audit = candidate / metadata.AUDIT
            audit.write_text(re.sub(r'(?m)^APPROVED_ADDITION_FINGERPRINT = "[0-9a-f]{64}"$',
                                    'APPROVED_ADDITION_FINGERPRINT = "' + '0' * 64 + '"', audit.read_text()))
            subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                            "commit", "-qam", "invalid derived pin"], cwd=candidate, check=True)
            head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=candidate, text=True).strip()
            paths = scope.changed_paths(candidate, self.base, head)
            self.assertEqual(metadata.filter_paths(candidate, self.base, head, paths)[0], [])
            checked = subprocess.run(["python3", "-B", str(ROOT / "scripts/quality/pr_fast_scope.py"),
                                      "--repo", str(candidate), "--base", self.base, "--head", head, "--frontend"],
                                     cwd=candidate, capture_output=True, text=True)
            self.assertNotEqual(checked.returncode, 0)
            self.assertIn("fingerprint mismatch", checked.stderr)

    def test_mixed_business_change_is_retained(self):
        paths = scope.changed_paths(self.repo, self.base, self.head) + ['internal/runtime/lease.go', 'frontend/App.tsx']
        filtered, ignored = metadata.filter_paths(self.repo, self.base, self.head, paths)
        self.assertEqual(filtered, ['internal/runtime/lease.go', 'frontend/App.tsx'])
        self.assertEqual(len(ignored), 7)

    def test_dependency_script_identity_auditor_and_provenance_edits_are_not_hidden(self):
        original_snapshot = metadata.snapshot
        mutations = [
            ('frontend/package.json', lambda raw: json.dumps({**json.loads(raw), 'scripts': {'test': 'true'}}).encode()),
            ('frontend/package-lock.json', lambda raw: json.dumps({**json.loads(raw), 'lockfileVersion': 99}).encode()),
            ('product-identity.json', lambda raw: json.dumps({**json.loads(raw), 'display_name': 'unreviewed'}).encode()),
            (metadata.AUDIT, lambda raw: raw + b'\nprint("unreviewed code")\n'),
            (metadata.MIGRATION, lambda raw: json.dumps({**json.loads(raw), 'unreviewed': True}).encode()),
            (metadata.LICENSES, lambda raw: json.dumps({**json.loads(raw), 'unreviewed': True}).encode()),
            (metadata.MATRIX, lambda raw: raw + b'\n'),
        ]
        for target, mutate in mutations:
            def changed(repo, revision, path):
                raw = original_snapshot(repo, revision, path)
                return mutate(raw) if revision == self.head and path == target else raw
            with self.subTest(target=target), patch.object(metadata, 'snapshot', side_effect=changed):
                self.assertEqual(metadata.metadata_paths(self.repo, self.base, self.head), set())

    def test_invalid_same_or_initial_base_never_skips(self):
        for base, head in [(self.head, self.head), ('0' * 40, self.head), ('main', self.head)]:
            with self.subTest(base=base):
                self.assertEqual(metadata.metadata_paths(self.repo, base, head), set())

    def test_mixed_dependency_delta_ignores_only_independently_proved_identity_version(self):
        original_snapshot = metadata.snapshot
        def changed(repo, revision, path):
            raw = original_snapshot(repo, revision, path)
            if revision == self.head and path == 'frontend/package-lock.json':
                lock = json.loads(raw)
                lock['packages']['node_modules/dompurify']['version'] = 'unreviewed-upgrade'
                return json.dumps(lock).encode()
            return raw
        paths = scope.changed_paths(self.repo, self.base, self.head)
        with patch.object(metadata, 'snapshot', side_effect=changed):
            filtered, ignored = metadata.filter_paths(self.repo, self.base, self.head, paths)
        self.assertEqual(ignored, ['product-identity.json'])
        self.assertEqual(filtered, [path for path in paths if path != 'product-identity.json'])
        self.assertIn('frontend/package-lock.json', filtered)
        self.assertTrue(scope.frontend_affected(filtered))

    def test_identity_name_schema_matrix_or_bad_counter_never_gets_partial_exemption(self):
        original_snapshot = metadata.snapshot
        mutations = [
            ('product-identity.json', lambda raw: json.dumps({**json.loads(raw), 'display_name': 'unreviewed'}).encode()),
            ('product-identity.json', lambda raw: json.dumps({**json.loads(raw), 'schema': 'unreviewed'}).encode()),
            ('product-identity.json', lambda raw: json.dumps({**json.loads(raw), 'version': '0.9.99'}).encode()),
            (metadata.MATRIX, lambda raw: raw + b'\n'),
        ]
        paths = scope.changed_paths(self.repo, self.base, self.head)
        for target, mutate in mutations:
            def changed(repo, revision, path):
                raw = original_snapshot(repo, revision, path)
                return mutate(raw) if revision == self.head and path == target else raw
            with self.subTest(target=target), patch.object(metadata, 'snapshot', side_effect=changed):
                self.assertEqual(metadata.filter_paths(self.repo, self.base, self.head, paths), (paths, []))

    def test_mixed_identity_proof_does_not_hide_runtime_inputs_or_auditor_code(self):
        original_snapshot = metadata.snapshot
        def changed(repo, revision, path):
            raw = original_snapshot(repo, revision, path)
            return raw + b'\nprint("unreviewed code")\n' if revision == self.head and path == metadata.AUDIT else raw
        paths = scope.changed_paths(self.repo, self.base, self.head) + ['assets/new/runtime.json', 'internal/runtime/lease.go']
        with patch.object(metadata, 'snapshot', side_effect=changed):
            filtered, ignored = metadata.filter_paths(self.repo, self.base, self.head, paths)
        self.assertEqual(ignored, ['product-identity.json'])
        self.assertIn(metadata.AUDIT, filtered)
        self.assertIn('assets/new/runtime.json', filtered)
        self.assertIn('internal/runtime/lease.go', filtered)


if __name__ == '__main__':
    unittest.main()
