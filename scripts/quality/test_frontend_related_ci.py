"""Focused workflow result/digest checks; synthetic evidence, no Node or CI."""
import copy
import os
import sys
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch

from scripts.quality import frontend_related_ci as ci
from scripts.quality import frontend_related_manifest as identity
from scripts.quality import frontend_related_partition as partition


class FrontendRelatedWorkflowTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='related-workflow-')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.context = {'execution_commit': 'a' * 40, 'execution_tree': 'c' * 40,
                        'base': 'b' * 40, 'pr_head': 'd' * 40, 'run_id': '100', 'run_attempt': '2'}
        self.source = {'head': 'a' * 40, 'tree': 'c' * 40, 'base': 'b' * 40}
        self.runtime = {'configuration': {'sha256': 'e' * 64}, 'node': 'locked'}
        self.binding = identity.execution_binding(self.source, self.context, self.runtime)
        specs = [{'project': name, 'path': 'tests/shared.test.ts', 'pool': 'forks'}
                 for name in ['node', 'dom']]
        self.plan = partition.plan(self.binding, specs, max_shards=4)
        self.bundle = {'schema': ci.SCHEMA, 'context': self.context, 'mode': 'sharded',
                       'prepared': True, 'source': self.source, 'runtime': self.runtime, 'plan': self.plan}
        self.plan_path = self.root / 'plan.json'
        self.reports = self.root / 'results'
        self.reports.mkdir()
        self.persist_plan()
        for part in self.plan['shards']:
            report = {'index': part['index'], 'binding': self.binding, 'exit_code': 0,
                      'report': {'planned': self.plan['specs'], 'queued': part['specs'],
                                 'ended': [{**item, 'state': 'skipped', 'ok': True} for item in part['specs']],
                                 'reason': 'passed', 'unhandled_errors': 0}}
            ci.write_json(self.reports / ('worker-' + str(part['index']) + '.json'), report)

    def persist_plan(self):
        self.plan_path.write_text(json.dumps(self.bundle), encoding='utf-8')
        self.plan_digest = ci.digest(self.plan_path)

    def aggregate(self, preparation='success', shards='success'):
        with patch.object(ci, 'context', return_value=self.context):
            ci.aggregate(self.root, self.plan_path, self.plan_digest, self.reports, preparation, shards)

    def test_cli_entry_removes_selector_read_token_before_any_phase(self):
        def prepare(_repo, _directory):
            self.assertNotIn('GITHUB_TOKEN', os.environ)
        with patch.dict(os.environ, {'GITHUB_TOKEN': 'synthetic-test-only'}), \
                patch.object(sys, 'argv', ['frontend_related_ci.py', 'prepare', '--directory', str(self.root / 'out')]), \
                patch.object(ci, 'prepare', side_effect=prepare):
            self.assertEqual(ci.main(), 0)

    def test_exact_ci_ownership_avoids_all_go_without_exempting_runtime_or_unknown_inputs(self):
        from scripts.quality import pr_fast_scope as scope
        repo = Path(__file__).resolve().parents[2]
        modules = ['frontend_related_ci.py', 'frontend_related_manifest.py', 'frontend_related_partition.py',
                   'relatedShardConfig.mjs', 'relatedShardDiscover.mjs', 'relatedShardIdentity.mjs',
                   'relatedShardReporter.mjs', 'relatedShardSequencer.mjs', 'test_frontend_related_ci.py',
                   'test_frontend_related_manifest.py', 'test_frontend_related_partition.py']
        paths = ['scripts/quality/' + name for name in modules]
        entries = [
            {'ImportPath': 'x-science/internal/kernel', 'Dir': str(repo / 'internal/kernel'), 'Imports': []},
            {'ImportPath': 'x-science/internal/server', 'Dir': str(repo / 'internal/server'),
             'Imports': ['x-science/internal/kernel']},
            {'ImportPath': 'x-science/internal/unrelated', 'Dir': str(repo / 'internal/unrelated'), 'Imports': []},
        ]
        selected, _ = scope.select_packages(repo, paths, entries)
        self.assertEqual(selected, [])
        groups = scope.verification_scope.matched(repo, paths)
        self.assertEqual([group['name'] for group in groups], ['verification-selection'])
        self.assertTrue(set(paths).issubset(groups[0]['paths']))
        self.assertFalse(groups[0]['frontend'])
        self.assertEqual(groups[0].get('packages', []), [])
        selected, _ = scope.select_packages(repo, ['internal/kernel/changed.go'], entries)
        self.assertEqual(selected, ['x-science/internal/kernel', 'x-science/internal/server'])
        for unknown in ['scripts/quality/unregistered-runtime.mjs', 'assets/unknown-runtime.json']:
            with self.subTest(unknown=unknown):
                selected, reason = scope.select_packages(repo, [unknown], entries)
                self.assertEqual(selected, sorted(entry['ImportPath'] for entry in entries))
                self.assertIn('conservative Go coverage', reason)
        frontend = 'frontend/packages/desktop/src/renderer/pages/library/LibraryPage.tsx'
        self.assertTrue(scope.frontend_affected([frontend], repo))

    def test_complete_exact_native_reports_accept_legal_skipped_modules(self):
        self.aggregate()

    def test_failed_cancelled_skipped_missing_and_duplicate_worker_cannot_pass(self):
        for status in ['failure', 'cancelled', 'skipped', '']:
            with self.subTest(status=status), self.assertRaises(ValueError):
                self.aggregate(shards=status)
        with self.assertRaises(ValueError):
            self.aggregate(preparation='failure')
        first = self.reports / 'worker-0.json'
        contents = first.read_bytes()
        first.unlink()
        with self.assertRaises(ValueError): self.aggregate()
        first.write_bytes(contents)
        ci.write_json(self.reports / 'worker-copy.json', ci.read_json(first))
        with self.assertRaises(ValueError): self.aggregate()

    def test_wrong_actual_config_source_tree_or_attempt_and_tampering_cannot_pass(self):
        original = copy.deepcopy(self.bundle)
        for place, key in [('context', 'run_attempt'), ('source', 'tree'), ('runtime', 'node')]:
            self.bundle = copy.deepcopy(original)
            self.bundle[place][key] = 'different'
            self.persist_plan()
            with self.subTest(place=place), self.assertRaises(ValueError): self.aggregate()
        self.bundle = original
        self.persist_plan()
        self.plan_path.write_text(self.plan_path.read_text() + ' ', encoding='utf-8')
        with self.assertRaises(ValueError): self.aggregate()

    def test_original_serial_empty_and_unsupported_semantics_require_real_prepare_success(self):
        self.bundle = {'schema': ci.SCHEMA, 'context': self.context, 'mode': 'serial', 'prepared': True}
        self.persist_plan()
        self.aggregate(shards='skipped')
        for preparation, shards in [('failure', 'skipped'), ('success', 'success'), ('success', 'cancelled')]:
            with self.subTest(preparation=preparation, shards=shards), self.assertRaises(ValueError):
                self.aggregate(preparation, shards)
        self.bundle['prepared'] = False
        self.persist_plan()
        with self.assertRaises(ValueError): self.aggregate(shards='skipped')

    def run_worker(self, *, wrong_configuration=False):
        from scripts.quality import pr_fast_scope as scope
        related = ['npx', '--no-install', 'vitest', 'related', '--run',
                   '--project', 'node', '--project', 'dom', 'packages/desktop/src/value.ts']
        configuration = {'sha256': 'e' * 64, 'default_reporters': ['default', 'github-actions']}
        self.runtime = {'command': related, 'configuration': configuration}
        self.binding = identity.execution_binding(self.source, self.context, self.runtime)
        self.plan = partition.plan(self.binding, self.plan['specs'], max_shards=4)
        self.bundle.update(runtime=self.runtime, plan=self.plan)
        self.persist_plan()
        repo = self.root / 'source-repo'
        repo.mkdir()
        output = self.root / 'new-worker'
        observed = {}

        def native(command, **options):
            observed['command'] = command
            self.assertFalse(any(arg.startswith('--sequence.sequencer=') for arg in command))
            wrapper = Path(next(arg.removeprefix('--config=') for arg in command if arg.startswith('--config=')))
            self.assertEqual(wrapper.read_text(), 'synthetic independently generated wrapper')
            self.assertIn('--reporter=github-actions', command)
            report = {'configuration_sha256': 'wrong' if wrong_configuration else 'e' * 64,
                      'planned': self.plan['specs'], 'queued': self.plan['shards'][0]['specs'],
                      'ended': [{**item, 'state': 'passed', 'ok': True}
                                for item in self.plan['shards'][0]['specs']],
                      'reason': 'passed', 'unhandled_errors': 0}
            ci.write_json(Path(options['env']['X_SCIENCE_RELATED_REPORT']), report)
            return subprocess.CompletedProcess(command, 0)

        with patch.object(ci, 'context', return_value=self.context), \
                patch.object(scope, 'changed_paths', return_value=[]), \
                patch.object(scope, 'behavior_scope', return_value=([], [], [])), \
                patch.object(scope.pr_metadata_scope, 'version_projection_paths', return_value=set()), \
                patch.object(scope.pr_frontend_scope, 'related_command', return_value=related), \
                patch.object(ci, 'discover', return_value={'eligible': True, 'specs': self.plan['specs'],
                    'configuration': configuration, 'wrapper_source': 'synthetic independently generated wrapper'}), \
                patch.object(ci, 'capture', return_value=(self.source, self.runtime, self.binding)), \
                patch.object(ci.subprocess, 'run', side_effect=native):
            ci.worker(repo, self.plan_path, self.plan_digest, output, 0)
        return output

    def test_worker_uses_class_wrapper_preserves_github_reporter_and_keeps_raw_observation(self):
        output = self.run_worker()
        self.assertEqual(ci.read_json(output / 'native-report.json')['configuration_sha256'], 'e' * 64)
        worker = ci.read_json(output / 'worker-0.json')
        self.assertNotIn('configuration_sha256', worker['report'])
        self.assertEqual(worker['exit_code'], 0)

    def test_zero_exit_with_wrong_effective_native_configuration_is_rejected(self):
        with self.assertRaises(ValueError):
            self.run_worker(wrong_configuration=True)

    def test_malformed_and_unexpected_plan_records_fail(self):
        for mutate in [lambda value: value.update(extra=True),
                       lambda value: value['plan']['shards'].reverse(),
                       lambda value: value['plan']['specs'].append(value['plan']['specs'][0])]:
            value = copy.deepcopy(self.bundle)
            mutate(value)
            with self.subTest(mutate=mutate), self.assertRaises(ValueError):
                ci.validate_bundle(value, self.context)
        self.plan_path.write_text('{"same": 1, "same": 2}', encoding='utf-8')
        with self.assertRaises(ValueError): ci.read_json(self.plan_path)


if __name__ == '__main__':
    unittest.main()
