"""Offline same-attempt frontend reuse and ordinary-fallback regressions."""
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

from scripts.quality import frontend_related_ci as related
from scripts.quality import frontend_related_manifest as identity
from scripts.quality import frontend_related_partition as partition
from scripts.quality import pr_fast_scope as scope
from scripts.quality import pr_frontend_reuse as reuse
from scripts.quality import pr_reuse_github as transport
from scripts.quality.test_pr_reuse_github import FakeTransport, REPOSITORY, REPO_ID, MAIN, BASE, HEAD, EXECUTION, TREE, PR, RUN, ATTEMPT


class FrontendTransport(FakeTransport):
    def __init__(self):
        super().__init__()
        self.command = ['npx', '--no-install', 'vitest', 'related', '--run', '--passWithNoTests',
                        '--project=node', '--project=dom', '--', 'src/changed.ts']
        context = {'repository_id': REPO_ID, 'repository': REPOSITORY, 'pull_request': PR, 'event': 'pull_request',
                   'base': BASE, 'pr_head': HEAD, 'execution_commit': EXECUTION, 'execution_tree': TREE,
                   'run_id': str(RUN), 'run_attempt': str(ATTEMPT),
                   'workflow_ref': REPOSITORY + f'/.github/workflows/quality-pr.yml@refs/pull/{PR}/merge',
                   'workflow_commit': EXECUTION}
        source = {'head': EXECUTION, 'base': BASE, 'tree': TREE, 'related_inputs': self.command[9:],
                  'vitest_version': '4.1.11', 'node_version': 'v22.23.4',
                  **{name: '1' * 64 for name in ['package_lock_sha256', 'vitest_config_sha256', 'vitest_package_sha256',
                                               'node_binary_sha256', 'generated_i18n_sha256']}}
        runtime = {'command': self.command, 'configuration': {'sha256': '2' * 64, 'record': {'env': 'exact'},
                                                           'default_reporters': ['default', 'github-actions']},
                   'npm_version': '10.9.9', 'installed_lock_sha256': '3' * 64, 'vite_package_sha256': '4' * 64}
        specs = [{'project': project, 'path': 'tests/related.test.ts', 'pool': 'forks'} for project in ['node', 'dom']]
        plan = partition.plan(identity.execution_binding(source, context, runtime), specs, max_shards=4)
        self.bundle = {'schema': related.SCHEMA, 'context': context, 'mode': 'sharded', 'prepared': True,
                       'source': source, 'runtime': runtime, 'plan': plan}
        self.checks, self.jobs, self.artifacts, self.archives = [], [], [], {}
        names = ['Merge Gate / CI required', 'PR frontend preparation', 'PR frontend scope', 'PR policy and static analysis']
        names += [f'PR related frontend partition ({i})' for i in range(2)]
        steps = ['Preserve frontend checks and plan exact related workers', 'Upload same-attempt related plan',
                 'Require original preparation success', 'Download same-attempt preparation',
                 'Download every current related result', 'Verify complete unique execution or original serial completion',
                 'Install original locked test runtime', 'Execute independently bound exact partition', 'Upload actual worker terminal evidence']
        for number, name in enumerate(names):
            jid = 4000 + number
            self.checks.append({'id': jid, 'name': name, 'head_sha': HEAD, 'status': 'completed', 'conclusion': 'success',
                                'app': {'id': 15368}, 'details_url': f'https://github.com/{REPOSITORY}/actions/runs/{RUN}/job/{jid}'})
            self.jobs.append({'id': jid, 'name': name, 'run_id': RUN, 'run_attempt': ATTEMPT,
                              'status': 'completed', 'conclusion': 'success', 'steps': [
                                  {'name': step, 'status': 'completed', 'conclusion': 'success'} for step in steps]})
        pending = {key: value for key, value in self.bundle.items() if key != 'prepared'}
        self.archive(3000, f'pr-frontend-related-plan-{RUN}-{ATTEMPT}', {
            'plan.json': self.bundle, 'pending-plan.json': pending, 'discovery-inputs.json': self.command[9:],
            'discovery.json': {'eligible': True, 'specs': specs, 'configuration': runtime['configuration']}})
        for index, shard in enumerate(plan['shards']):
            worker = {'index': index, 'binding': plan['binding'], 'exit_code': 0,
                      'report': {'planned': specs, 'queued': shard['specs'],
                                 'ended': [{**item, 'state': 'skipped', 'ok': True} for item in shard['specs']],
                                 'reason': 'passed', 'unhandled_errors': 0}}
            self.archive(3001 + index, f'pr-frontend-related-result-{RUN}-{ATTEMPT}-{index}', {f'worker-{index}.json': worker})

    def archive(self, identifier, name, files):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, 'w', zipfile.ZIP_DEFLATED) as archive:
            for path, value in files.items():
                archive.writestr(path, json.dumps(value))
        raw = buffer.getvalue()
        self.artifacts.append({'id': identifier, 'name': name, 'expired': False, 'size_in_bytes': len(raw),
                               'digest': 'sha256:' + reuse.digest(raw),
                               'workflow_run': {'id': RUN, 'repository_id': REPO_ID, 'head_repository_id': REPO_ID, 'head_sha': HEAD}})
        self.archives[identifier] = raw

    def receipts(self):
        return transport.fetch_merged_pr_receipts(REPOSITORY, MAIN, BASE, 'fixture-only', transport=self,
                                                 profile=transport.FRONTEND_PROFILE)


class FrontendReuseTests(unittest.TestCase):
    def setUp(self):
        self.fake = FrontendTransport()
        self.receipts = self.fake.receipts()
        self.current = {'repository_id': REPO_ID, 'repository': REPOSITORY, 'event': 'push', 'base': BASE,
                        'execution_commit': MAIN, 'execution_tree': TREE, 'run_id': '1000', 'run_attempt': '1',
                        'workflow_ref': REPOSITORY + '/.github/workflows/quality-main.yml@refs/heads/main', 'workflow_commit': MAIN}
        self.source = {**self.fake.bundle['source'], 'head': MAIN}
        self.runtime = copy.deepcopy(self.fake.bundle['runtime'])
        self.specs = copy.deepcopy(self.fake.bundle['plan']['specs'])

    def verify(self, receipts=None):
        return reuse.validate_equivalence(receipts or self.receipts, self.current, self.source, self.runtime,
                                          self.specs, self.fake.command)

    def test_complete_original_execution_is_referenced_without_rebinding(self):
        result = self.verify()
        self.assertTrue(result['tests_not_reexecuted'])
        self.assertEqual(result['main_context']['execution_commit'], MAIN)
        self.assertEqual(result['origin_execution_context']['execution_commit'], EXECUTION)
        self.assertEqual(result['origin_execution_context']['run_attempt'], str(ATTEMPT))
        self.assertEqual(len(result['origin_artifacts']), 3)

    def test_pure_fetch_seal_then_independent_capture_records_main_reference(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            repo = root / 'source'
            repo.mkdir()
            directory = root / 'evidence'
            with patch.object(reuse, 'main_identity', return_value=self.current), \
                    patch.object(reuse, 'unchanged_ci_authority'), \
                    patch.object(reuse, 'git', side_effect=lambda _repo, *args: 'frontend/src/changed.ts' if args[0] == 'diff' else ''), \
                    patch.object(transport, 'fetch_merged_pr_receipts', return_value=self.receipts) as fetch:
                path = reuse.fetch_origin(repo, BASE, MAIN, directory, 'fixture-only')
                fetch.assert_called_once()
                self.assertEqual(fetch.call_args.kwargs['profile'], transport.FRONTEND_PROFILE)
            seal = related.digest(path)
            with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GITHUB_EVENT_NAME': 'push'}), \
                    patch.object(reuse, 'main_identity', return_value=self.current), \
                    patch.object(reuse, 'unchanged_ci_authority'), patch.object(identity, '_require_source_state'), \
                    patch.object(reuse, 'bounded_capture', return_value={
                        'source': self.source, 'runtime': self.runtime, 'specs': self.specs}):
                self.assertTrue(reuse.try_reuse(repo, BASE, MAIN, self.fake.command, directory, seal))
            verdict = related.read_json(directory / 'verified-reuse.json')
            self.assertEqual(verdict['origin_execution_context']['execution_commit'], EXECUTION)
            self.assertEqual(verdict['main_context']['execution_commit'], MAIN)
            origin = related.read_json(path)
            self.assertEqual(origin['receipts']['run']['attempt'], ATTEMPT)
            self.assertEqual(related.digest(path), seal)

    def test_new_paths_have_exact_verification_ownership_without_unknown_path_exemption(self):
        repo = Path(__file__).resolve().parents[2]
        paths = ['scripts/quality/pr_frontend_reuse.py', 'scripts/quality/test_pr_frontend_reuse.py']
        groups = scope.verification_scope.matched(repo, paths)
        self.assertEqual([item['name'] for item in groups], ['verification-selection'])
        self.assertIn('scripts.quality.test_pr_frontend_reuse', groups[0]['checks'][0])
        entries = [{'ImportPath': 'example/server', 'Dir': str(repo / 'internal/server'), 'Imports': []}]
        self.assertEqual(scope.select_packages(repo, paths, entries)[0], [])
        self.assertEqual(scope.select_packages(repo, ['scripts/quality/unknown_runtime.py'], entries)[0], ['example/server'])

    def test_missing_duplicate_mixed_attempt_or_failed_worker_is_rejected(self):
        for kind in ['missing', 'duplicate', 'attempt', 'failed', 'unhandled', 'coverage', 'inventory']:
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                receipts = copy.deepcopy(self.receipts)
                if kind == 'missing': receipts['artifacts'].pop()
                elif kind == 'duplicate': receipts['artifacts'].append(copy.deepcopy(receipts['artifacts'][-1]))
                elif kind == 'attempt': receipts['artifacts'][-1]['name'] = 'pr-frontend-related-result-900-1-1'
                else:
                    member = receipts['artifacts'][-1]['files']
                    value = json.loads(member['worker-1.json'])
                    if kind == 'failed': value['exit_code'] = 1
                    if kind == 'unhandled': value['report']['unhandled_errors'] = 1
                    if kind == 'coverage': value['report']['ended'] = []
                    if kind == 'inventory': value['report']['planned'] = []
                    member['worker-1.json'] = json.dumps(value).encode()
                self.verify(receipts)

    def test_source_dependencies_environment_config_and_unknown_capture_fall_back(self):
        for field in ['node_binary_sha256', 'package_lock_sha256', 'generated_i18n_sha256', 'vitest_config_sha256', 'tree']:
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.source = {**self.fake.bundle['source'], 'head': MAIN, field: 'changed'}
                self.verify()
        self.source = {**self.fake.bundle['source'], 'head': MAIN}
        for field in ['npm_version', 'installed_lock_sha256', 'configuration']:
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.runtime = {**self.fake.bundle['runtime'], field: 'changed'}
                self.verify()
        self.runtime = copy.deepcopy(self.fake.bundle['runtime'])
        self.runtime['unknown'] = True
        with self.assertRaises(ValueError): self.verify()

    def test_authenticated_fetch_rejects_foreign_app_attempt_step_and_archive_set(self):
        for kind in ['app', 'attempt', 'step', 'missing', 'extra', 'digest', 'newer-failed']:
            fake = FrontendTransport()
            if kind == 'app': fake.checks[1]['app']['id'] = 999
            if kind == 'attempt': fake.jobs[-1]['run_attempt'] = 1
            if kind == 'step': fake.jobs[-1]['steps'][-1]['conclusion'] = 'skipped'
            if kind == 'missing': fake.artifacts.pop()
            if kind == 'extra': fake.artifacts.append({**fake.artifacts[-1], 'name': 'pr-frontend-related-result-900-2-3'})
            if kind == 'digest': fake.artifacts[0]['digest'] = 'sha256:' + '0' * 64
            if kind == 'newer-failed': fake.extra_runs = [{**fake.run, 'id': RUN + 1, 'conclusion': 'failure'}]
            with self.subTest(kind=kind), self.assertRaises(RuntimeError): fake.receipts()

    def test_seal_tampering_and_missing_receipts_return_original_path(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            directory = root / 'external'
            directory.mkdir()
            related.write_json(directory / 'origin.json', {'schema': reuse.ORIGIN_SCHEMA, 'status': 'fallback'})
            with self.assertRaises(ValueError): reuse.load_origin(root / 'source', directory, '0' * 64, self.current)
            with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'GITHUB_EVENT_NAME': 'push'}), \
                    patch.object(reuse, 'main_identity', return_value=self.current), \
                    patch.object(reuse, 'unchanged_ci_authority'), patch.object(reuse, 'bounded_capture') as capture:
                self.assertFalse(reuse.try_reuse(root / 'source', BASE, MAIN, self.fake.command, directory, '0' * 64))
                capture.assert_not_called()
                self.assertEqual(related.read_json(directory / 'reuse-decision.json')['reason'], 'sealed_origin')

    def test_unknown_or_ambiguous_json_never_becomes_original_execution_evidence(self):
        for raw in [b'{"attempt":1,"attempt":2}', b'{"value":NaN}', b'{"value":Infinity}']:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                reuse.decode(raw)

    def test_bounded_capture_reaps_only_its_owned_group_before_fallback(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(reuse, 'CAN_REAP_PROCESS_GROUP', True), \
                patch.object(reuse.signal, 'SIGKILL', 9, create=True), \
                patch.object(reuse.subprocess, 'Popen') as popen, patch.object(reuse.os, 'killpg', create=True) as kill:
            child = popen.return_value
            child.pid = 12345
            child.wait.side_effect = [subprocess.TimeoutExpired('fixture', 25), 0]
            with self.assertRaises(ValueError):
                reuse.bounded_capture(Path(temporary), self.current, self.fake.command, Path(temporary))
            kill.assert_called_once_with(12345, reuse.signal.SIGKILL)
            self.assertEqual(child.wait.call_count, 2)
            self.assertNotIn('GITHUB_TOKEN', popen.call_args.kwargs['env'])

    def test_fetch_cli_consumes_token_only_in_the_pure_reader(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            receipt = directory / 'origin.json'
            receipt.write_text('{}', encoding='utf-8')
            output = directory / 'output'
            def fetch(_repo, _base, _head, _directory, token):
                self.assertEqual(token, 'synthetic-read-token')
                self.assertNotIn('GITHUB_TOKEN', os.environ)
                return receipt
            with patch.dict(os.environ, {'GITHUB_TOKEN': 'synthetic-read-token', 'GITHUB_OUTPUT': str(output)}), \
                    patch.object(sys, 'argv', ['reuse', 'fetch', '--base', BASE, '--head', MAIN, '--directory', str(directory)]), \
                    patch.object(reuse, 'fetch_origin', side_effect=fetch):
                self.assertEqual(reuse.main(), 0)
            self.assertEqual(output.read_text(), 'receipt-sha256=' + related.digest(receipt) + '\n')

    def test_main_selector_changes_only_the_related_command_and_failure_runs_original(self):
        for accepted in [True, False]:
            commands = []
            def run(repo, *command):
                commands.append(list(command))
                return subprocess.CompletedProcess(command, 0)
            with self.subTest(accepted=accepted), \
                    patch.object(sys, 'argv', ['scope', '--frontend', '--base', BASE, '--head', MAIN,
                                              '--frontend-reuse-dir', '/external', '--frontend-reuse-digest', '0' * 64]), \
                    patch.object(scope, 'changed_paths', return_value=['frontend/src/changed.ts']), \
                    patch.object(scope, 'behavior_scope', return_value=(['frontend/src/changed.ts'], [], [])), \
                    patch.object(scope.verification_scope, 'matched', return_value=[]), \
                    patch.object(scope.pr_metadata_scope, 'version_projection_paths', return_value=[]), \
                    patch.object(scope.pr_frontend_scope, 'related_command', return_value=self.fake.command), \
                    patch.object(scope.pr_frontend_scope, 'fixture_command', return_value=None), \
                    patch.object(scope.pr_frontend_scope, 'harness_command', return_value=None), \
                    patch.object(scope.pr_frontend_scope, 'snapshot_inputs', return_value=[]), \
                    patch.object(scope, 'run', side_effect=run), patch.object(reuse, 'try_reuse', return_value=accepted):
                self.assertEqual(scope.main(), 0)
            self.assertEqual(commands.count(self.fake.command), 0 if accepted else 1)
            for command in [['npm', 'ci', '--ignore-scripts'], ['npm', 'run', 'typecheck'], ['npm', 'run', 'lint'],
                            ['npm', 'run', 'format:check'], ['npm', 'run', 'build'], ['npm', 'run', 'test:packaged']]:
                self.assertIn(command, commands)


if __name__ == '__main__':
    unittest.main()
