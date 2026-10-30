"""Isolated Git fixture for local related-input evidence; no real CI or tasks."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.quality import frontend_related_manifest as subject


class FrontendRelatedManifestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='xscience-related-manifest-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git('init', '-q')
        for name, body in {
            '.gitignore': 'frontend/node_modules/\n',
            'frontend/package-lock.json': '{"lockfileVersion":3}\n',
            'frontend/vitest.config.ts': 'export default {}\n',
            'frontend/src/example.ts': 'export const a = 1\n',
            subject.GENERATED_I18N: 'export type Key = never;\n',
        }.items():
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(body, encoding='utf-8')
        self.git('add', '.')
        self.git('-c', 'user.name=Synthetic', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'fixture')
        self.head = self.git('rev-parse', 'HEAD')
        self.vitest = self.root / 'frontend/node_modules/vitest/package.json'
        self.vitest.parent.mkdir(parents=True)
        self.vitest.write_text(json.dumps({'version': '4.1.11'}), encoding='utf-8')

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.PIPE).decode().strip()

    def capture(self, inputs=None, head=None):
        return subject.capture(self.root, head=head or self.head, base=self.head,
                               inputs=inputs or ['src/example.ts'], node=Path(sys.executable))

    def test_exact_clean_checkout_produces_deterministic_source_binding(self):
        first = self.capture(['tests/other.test.ts', 'src/example.ts'])
        second = self.capture(['src/example.ts', 'tests/other.test.ts'])
        self.assertEqual(first, second)
        self.assertEqual(first['record']['tree'], self.git('rev-parse', 'HEAD^{tree}'))
        self.assertEqual(first['binding']['head'], self.head)

    def test_dirty_source_config_or_lock_and_wrong_head_are_rejected(self):
        for name in ('frontend/src/example.ts', 'frontend/vitest.config.ts', 'frontend/package-lock.json'):
            target = self.root / name
            original = target.read_bytes()
            target.write_bytes(original + b'changed')
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.capture()
            target.write_bytes(original)
        with self.assertRaises(ValueError):
            self.capture(head='f' * 40)
        with self.assertRaises(ValueError):
            self.capture(head='f' * 41)

    def test_vitest_metadata_and_selector_change_the_binding(self):
        first = self.capture()
        self.vitest.write_text(json.dumps({'version': '4.1.12'}), encoding='utf-8')
        self.assertNotEqual(first['binding'], self.capture()['binding'])
        self.assertNotEqual(first['binding'], self.capture(['src/other.ts'])['binding'])

    def test_source_change_during_capture_is_rejected(self):
        original = subject._run
        statuses = 0

        def changed(repo, *args):
            nonlocal statuses
            if args[:2] == ('git', 'status'):
                statuses += 1
                if statuses == 2:
                    return b' M frontend/src/example.ts\0'
            return original(repo, *args)

        with patch.object(subject, '_run', side_effect=changed), self.assertRaises(ValueError):
            self.capture()

    def test_only_actual_generated_i18n_change_is_allowed_and_bound(self):
        target = self.root / subject.GENERATED_I18N
        before = subject.capture(self.root, head=self.head, base=self.head,
                                 inputs=['src/example.ts'], node=Path(sys.executable), allow_generated_i18n=True)
        target.write_text('export type Key = "new.key";\n', encoding='utf-8')
        after = subject.capture(self.root, head=self.head, base=self.head,
                                inputs=['src/example.ts'], node=Path(sys.executable), allow_generated_i18n=True)
        self.assertNotEqual(before['record']['generated_i18n_sha256'], after['record']['generated_i18n_sha256'])
        for other in ['frontend/src/example.ts', 'frontend/untracked.ts']:
            path = self.root / other
            existed = path.exists(); original = path.read_bytes() if existed else b''
            path.write_bytes(original + b'changed')
            with self.subTest(path=other), self.assertRaises(ValueError):
                subject.capture(self.root, head=self.head, base=self.head,
                                inputs=['src/example.ts'], node=Path(sys.executable), allow_generated_i18n=True)
            if existed: path.write_bytes(original)
            else: path.unlink()
        self.git('add', subject.GENERATED_I18N)
        with self.assertRaises(ValueError):
            subject.capture(self.root, head=self.head, base=self.head,
                            inputs=['src/example.ts'], node=Path(sys.executable), allow_generated_i18n=True)

    def test_invalid_or_duplicate_paths_are_rejected(self):
        for paths in (['../outside'], ['/tmp/file'], ['src/a.ts', 'src/a.ts'], ['src\\a.ts']):
            with self.subTest(paths=paths), self.assertRaises(ValueError):
                self.capture(paths)


class GithubExecutionIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='related-event-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.event = self.root / 'event.json'
        self.body = {'repository': {'id': 1374130212, 'full_name': 'Victor-Xu-1/X-Science'},
                     'number': 641, 'pull_request': {'number': 641,
                     'base': {'sha': 'b' * 40}, 'head': {'sha': 'a' * 40}}}
        self.env = {'GITHUB_REPOSITORY_ID': '1374130212', 'GITHUB_REPOSITORY': 'Victor-Xu-1/X-Science',
                    'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_EVENT_PATH': str(self.event),
                    'GITHUB_SHA': 'c' * 40, 'GITHUB_REF': 'refs/pull/641/merge',
                    'GITHUB_RUN_ID': '41000000000', 'GITHUB_RUN_ATTEMPT': '2',
                    'GITHUB_WORKFLOW_REF': 'Victor-Xu-1/X-Science/.github/workflows/quality-pr.yml@refs/pull/641/merge',
                    'GITHUB_WORKFLOW_SHA': 'c' * 40, 'GITHUB_JOB': 'pr-frontend-plan'}

    def identity(self, *, parents=None, job='pr-frontend-plan'):
        self.event.write_text(json.dumps(self.body), encoding='utf-8')
        def git(_repo, *args):
            if args == ('git', 'rev-parse', 'HEAD'): return b'c' * 40
            if args == ('git', 'rev-parse', 'HEAD^{tree}'): return b'd' * 40
            if args == ('git', 'rev-list', '--parents', '-n', '1', 'HEAD'):
                return (parents or ' '.join([letter * 40 for letter in ['c', 'b', 'a']])).encode()
            raise AssertionError(args)
        with patch.object(subject, '_run', side_effect=git):
            return subject.github_execution_identity(self.root, self.env, expected_job=job)

    def test_distinguishes_pr_head_and_actual_merge_execution_and_attempt(self):
        record = self.identity()
        self.assertEqual(record['pr_head'], 'a' * 40)
        self.assertEqual(record['base'], 'b' * 40)
        self.assertEqual(record['execution_commit'], 'c' * 40)
        self.assertEqual(record['execution_tree'], 'd' * 40)
        self.assertEqual(record['run_attempt'], '2')
        self.env['GITHUB_JOB'] = 'pr-frontend-related-shards'
        self.assertEqual(record, self.identity(job='pr-frontend-related-shards'))

    def test_wrong_event_repository_commit_workflow_and_job_fail(self):
        bad = {'GITHUB_REPOSITORY_ID': '1', 'GITHUB_REPOSITORY': 'other/X-Science',
               'GITHUB_EVENT_NAME': 'pull_request_target', 'GITHUB_SHA': 'a' * 40,
               'GITHUB_REF': 'refs/pull/640/merge', 'GITHUB_RUN_ID': '0',
               'GITHUB_RUN_ATTEMPT': '0', 'GITHUB_WORKFLOW_SHA': 'invalid',
               'GITHUB_WORKFLOW_REF': 'other/workflow.yml@refs/heads/main',
               'GITHUB_JOB': 'unknown'}
        for key, value in bad.items():
            with self.subTest(key=key), patch.dict(self.env, {key: value}), self.assertRaises(ValueError):
                self.identity()

    def test_payload_or_merge_parent_mismatch_and_oversized_event_fail(self):
        for location, key, wrong in [(self.body['repository'], 'id', 1),
                                     (self.body['pull_request']['base'], 'sha', 'e' * 40),
                                     (self.body['pull_request'], 'number', 640)]:
            before = location[key]; location[key] = wrong
            with self.subTest(key=key), self.assertRaises(ValueError): self.identity()
            location[key] = before
        with self.assertRaises(ValueError):
            self.identity(parents=' '.join(['c' * 40, 'b' * 40, 'e' * 40]))
        self.body['irrelevant'] = 'x' * (2 * 1024 * 1024)
        with self.assertRaises(ValueError): self.identity()


class ExecutionBindingTests(unittest.TestCase):
    def test_every_execution_source_config_and_toolchain_fact_changes_the_binding(self):
        source = {'head': 'a' * 40, 'base': 'b' * 40, 'tree': 'c' * 40, 'package_lock_sha256': 'd' * 64}
        context = {'execution_commit': 'a' * 40, 'base': 'b' * 40, 'execution_tree': 'c' * 40,
                   'pr_head': 'e' * 40, 'run_id': '100', 'run_attempt': '1'}
        runtime = {'resolved_config': 'cfg', 'node_binary': 'node', 'npm': 'npm', 'related': ['src/a.ts']}
        good = subject.execution_binding(source, context, runtime)
        for target in [source, context, runtime]:
            for key in list(target):
                if key in ['head', 'base', 'tree', 'execution_commit', 'execution_tree']: continue
                old = target[key]; target[key] = ['different']
                with self.subTest(key=key):
                    self.assertNotEqual(good, subject.execution_binding(source, context, runtime))
                target[key] = old
        for key in ['execution_commit', 'execution_tree', 'base']:
            old = context[key]; context[key] = 'f' * 40
            with self.subTest(key=key), self.assertRaises(ValueError):
                subject.execution_binding(source, context, runtime)
            context[key] = old
        with self.assertRaises(ValueError):
            subject.execution_binding(source, context, {'not_json': object()})
        with self.assertRaises(ValueError):
            subject.execution_binding(source, context, {'invalid_number': float('nan')})


if __name__ == '__main__':
    unittest.main()
