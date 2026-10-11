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
from scripts.quality import product_identity_gate as identity

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
        self.assertEqual(len(ignored), 8)

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
        self.assertEqual(len(ignored), 8)

    def test_other_consumer_disposition_change_is_not_version_metadata(self):
        original_snapshot = metadata.snapshot
        def changed(repo, revision, path):
            raw = original_snapshot(repo, revision, path)
            if revision == self.head and path == metadata.MATRIX:
                value = json.loads(raw)
                value["consumer_inventory"][0]["current_disposition"] = "reviewed disposition-only edit"
                return json.dumps(value).encode()
            return raw
        with patch.object(metadata, 'snapshot', side_effect=changed):
            paths = scope.changed_paths(self.repo, self.base, self.head)
            self.assertEqual(metadata.filter_paths(self.repo, self.base, self.head, paths), (paths, []))
            self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head), set())

    def test_changed_provenance_without_counter_delta_still_requires_fresh_audit(self):
        with tempfile.TemporaryDirectory(prefix='metadata-changed-provenance-') as directory:
            candidate = Path(directory) / 'candidate'
            subprocess.run(['git', 'clone', '--quiet', '--shared', '--no-hardlinks',
                            str(self.repo), str(candidate)], check=True)
            manifest = candidate / metadata.MIGRATION
            value = json.loads(manifest.read_bytes())
            value['unreviewed-source-record'] = True
            manifest.write_text(json.dumps(value))
            subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                            'commit', '-qam', 'changed provenance only'], cwd=candidate, check=True)
            head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=candidate, text=True).strip()
            self.assertEqual(scope.changed_paths(candidate, self.head, head), [metadata.MIGRATION])
            argv = ['pr_fast_scope.py', '--repo', str(candidate), '--base', self.head, '--head', head, '--frontend']
            with patch.object(sys, 'argv', argv), patch.object(
                scope, 'run', return_value=subprocess.CompletedProcess([], 19)) as invoke:
                self.assertEqual(scope.main(), 19)
                self.assertEqual(invoke.call_count, 1)
                self.assertIn('audit_frontend_migration.py', invoke.call_args.args[2])

    def test_counter_projections_stay_inert_when_source_provenance_also_changes(self):
        original_snapshot = metadata.snapshot
        def changed(repo, revision, path):
            raw = original_snapshot(repo, revision, path)
            return json.dumps({**json.loads(raw), 'new-source-record': True}).encode() if revision == self.head and path == metadata.MIGRATION else raw
        with patch.object(metadata, 'snapshot', side_effect=changed):
            self.assertEqual(metadata.metadata_paths(self.repo, self.base, self.head), set())
            self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head),
                             {'product-identity.json', 'frontend/package.json', 'frontend/package-lock.json',
                              'frontend/packages/desktop/package.json'})

    def test_projection_selection_never_hides_manifest_behavior_or_dependency_edits(self):
        original_snapshot = metadata.snapshot
        for target in ['frontend/package.json', 'frontend/package-lock.json', metadata.MATRIX, metadata.IDENTITY]:
            def changed(repo, revision, path):
                raw = original_snapshot(repo, revision, path)
                return json.dumps({**json.loads(raw), 'unreviewed': True}).encode() if revision == self.head and path == target else raw
            with self.subTest(target=target), patch.object(metadata, 'snapshot', side_effect=changed):
                self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head), set())

    def test_dependency_script_identity_auditor_and_provenance_edits_are_not_hidden(self):
        original_snapshot = metadata.snapshot
        mutations = [
            ('frontend/package.json', lambda raw: json.dumps({**json.loads(raw), 'scripts': {'test': 'true'}}).encode()),
            ('frontend/package-lock.json', lambda raw: json.dumps({**json.loads(raw), 'lockfileVersion': 99}).encode()),
            ('product-identity.json', lambda raw: json.dumps({**json.loads(raw), 'display_name': 'unreviewed'}).encode()),
            (metadata.AUDIT, lambda raw: raw + b'\nprint("unreviewed code")\n'),
            (metadata.MIGRATION, lambda raw: json.dumps({**json.loads(raw), 'unreviewed': True}).encode()),
            (metadata.LICENSES, lambda raw: json.dumps({**json.loads(raw), 'unreviewed': True}).encode()),
            (metadata.NOTICES, lambda raw: raw + b'\nunreviewed notice text\n'),
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

    def schema_transition(self, mutate=None):
        original = metadata.snapshot
        reference_path = 'docs/governance/product-identity.json'
        schema_path = 'internal/persistence/workspace/versioned_schema.go'
        old = json.loads(original(self.repo, self.base, reference_path))['current_workspace_schema']['target']
        new = old + 1

        def snapshot(repo, revision, path):
            raw = original(repo, revision, path)
            if revision != self.head:
                return raw
            if path == metadata.MATRIX:
                value = json.loads(raw)
                for item in value['consumer_inventory']:
                    if item['surface_id'] == 'workspace-schema-and-contracts':
                        item['current_disposition'] = f'production workspace schema target is {new} with no pending schema cohort'
                raw = json.dumps(value).encode()
            elif path == reference_path:
                value = json.loads(raw)
                value['current_workspace_schema']['target'] = new
                raw = json.dumps(value).encode()
            elif path == schema_path:
                raw = identity.SCHEMA_CONST.sub(f'const workspaceSchemaVersion = {new}', raw.decode()).encode()
            return mutate(path, raw) if mutate else raw
        return snapshot

    def test_synchronized_schema_declaration_keeps_only_counter_metadata_inert(self):
        extra = ['docs/governance/product-identity.json', metadata.MATRIX,
                 'internal/persistence/workspace/versioned_schema.go']
        paths = scope.changed_paths(self.repo, self.base, self.head) + extra
        with patch.object(metadata, 'snapshot', side_effect=self.schema_transition()):
            filtered, ignored = metadata.filter_paths(self.repo, self.base, self.head, paths)
            self.assertEqual(filtered, extra)
            self.assertEqual(len(ignored), 8)
            self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head),
                             {'product-identity.json', 'frontend/package.json', 'frontend/package-lock.json',
                              'frontend/packages/desktop/package.json'})
        self.assertFalse(scope.frontend_affected(filtered, self.repo))
        groups = scope.verification_scope.matched(self.repo, filtered)
        self.assertIn('schema-and-package-contracts', [group['name'] for group in groups])

    def test_schema_transition_frontend_stage_requires_fresh_provenance_without_npm(self):
        paths = scope.changed_paths(self.repo, self.base, self.head) + [
            metadata.MATRIX, 'docs/governance/product-identity.json',
            'internal/persistence/workspace/versioned_schema.go']
        argv = ['pr_fast_scope.py', '--repo', str(self.repo), '--base', self.base, '--head', self.head, '--frontend']
        for result in [0, 19]:
            with self.subTest(provenance_result=result), patch.object(sys, 'argv', argv), patch.object(
                metadata, 'snapshot', side_effect=self.schema_transition()), patch.object(
                scope, 'changed_paths', return_value=paths), patch.object(
                scope, 'run', return_value=subprocess.CompletedProcess([], result)) as invoke:
                self.assertEqual(scope.main(), result)
                self.assertEqual(invoke.call_count, 1)
                self.assertIn('audit_frontend_migration.py', invoke.call_args.args[2])

    def test_schema_transition_never_exempts_unknown_matrix_authority_or_consumers(self):
        def alter_matrix(mutator):
            def change(path, raw):
                if path == metadata.MATRIX:
                    value = json.loads(raw)
                    mutator(value)
                    return json.dumps(value).encode()
                return raw
            return change

        def workspace(value):
            return next(item for item in value['consumer_inventory']
                        if item['surface_id'] == 'workspace-schema-and-contracts')

        mutations = [
            ('authority', lambda value: value.update(authority_path='unreviewed.json')),
            ('projection', lambda value: value['product_version_projections'].append(
                {'path': 'unreviewed.json', 'kind': 'json-pointer', 'pointer': '/version'})),
            ('other consumer', lambda value: value['consumer_inventory'][0].update(target='unreviewed')),
            ('stanza owner', lambda value: workspace(value).update(owner='core-runtime')),
            ('stanza path', lambda value: workspace(value)['paths'].append('unreviewed.json')),
            ('control flag', lambda value: workspace(value).update(release_blocking=True)),
            ('duplicate stanza', lambda value: value['consumer_inventory'].append(dict(workspace(value)))),
            ('wording', lambda value: workspace(value).update(current_disposition='compiled target is current')),
        ]
        paths = scope.changed_paths(self.repo, self.base, self.head)
        for name, mutate in mutations:
            with self.subTest(name=name), patch.object(metadata, 'snapshot', side_effect=self.schema_transition(alter_matrix(mutate))):
                self.assertEqual(metadata.filter_paths(self.repo, self.base, self.head, paths), (paths, []))
                self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head), set())

    def test_schema_transition_rejects_unsynchronized_or_modified_validators(self):
        def wrong_reference(path, raw):
            if path == 'docs/governance/product-identity.json':
                value = json.loads(raw)
                value['current_workspace_schema']['target'] += 1
                return json.dumps(value).encode()
            return raw

        def changed_rule(path, raw):
            if path == 'docs/governance/product-identity.json':
                value = json.loads(raw)
                value['rules'].append('unreviewed authority rule')
                return json.dumps(value).encode()
            return raw

        def bad_go(path, raw):
            return b'const workspaceSchemaVersion = 0\n' if path == 'internal/persistence/workspace/versioned_schema.go' else raw

        def changed_validator(path, raw):
            return raw + b'\n# changed validation implementation\n' if path == 'scripts/quality/product_identity_gate.py' else raw

        original = metadata.snapshot
        def missing_matrix_update(path, raw):
            return original(self.repo, self.head, path) if path == metadata.MATRIX else raw

        def missing_reference_update(path, raw):
            return original(self.repo, self.head, path) if path == 'docs/governance/product-identity.json' else raw

        def missing_const_update(path, raw):
            return original(self.repo, self.head, path) if path == 'internal/persistence/workspace/versioned_schema.go' else raw

        paths = scope.changed_paths(self.repo, self.base, self.head)
        for mutate in [wrong_reference, changed_rule, bad_go, changed_validator,
                       missing_matrix_update, missing_reference_update, missing_const_update]:
            with self.subTest(mutate=mutate.__name__), patch.object(metadata, 'snapshot', side_effect=self.schema_transition(mutate)):
                self.assertEqual(metadata.filter_paths(self.repo, self.base, self.head, paths), (paths, []))
                self.assertEqual(metadata.version_projection_paths(self.repo, self.base, self.head), set())

    def test_schema_transition_rejects_loaded_validation_source_drift(self):
        with tempfile.TemporaryDirectory(prefix='schema-validator-drift-') as directory:
            source = Path(directory) / 'product_identity_gate.py'
            source.write_bytes(Path(identity.__file__).read_bytes() + b'\n# uncommitted validation drift\n')
            paths = scope.changed_paths(self.repo, self.base, self.head)
            with patch.object(identity, '__file__', str(source)), patch.object(
                metadata, 'snapshot', side_effect=self.schema_transition()):
                self.assertEqual(metadata.filter_paths(self.repo, self.base, self.head, paths), (paths, []))


if __name__ == '__main__':
    unittest.main()
