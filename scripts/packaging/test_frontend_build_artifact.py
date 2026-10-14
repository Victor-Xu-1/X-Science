import copy
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from scripts.packaging import frontend_build_artifact as subject

WORKFLOW = '.github/workflows/frontend-build-artifact.yml'
TOOLS = {'node': 'v22.22.2', 'npm': '10.9.0', 'git': 'git fixture',
         'python': '3.12.0', 'system': 'Linux', 'machine': 'x86_64'}


def request(sha='a' * 40):
    return ({'repository': {'id': subject.REPOSITORY_ID, 'full_name': subject.REPOSITORY},
             'inputs': {'expected_sha': sha}},
            {'GITHUB_REPOSITORY': subject.REPOSITORY, 'GITHUB_REPOSITORY_ID': str(subject.REPOSITORY_ID),
             'GITHUB_EVENT_NAME': 'workflow_dispatch', 'GITHUB_REF': 'refs/heads/main',
             'GITHUB_SHA': sha, 'GITHUB_RUN_ID': '123', 'GITHUB_RUN_ATTEMPT': '2'})


class FrontendBuildRequestTests(unittest.TestCase):
    def test_exact_main_dispatch_coordinates(self):
        event, environment = request()
        sha, producer = subject.validate_request(event, environment)
        self.assertEqual(sha, 'a' * 40)
        self.assertEqual(producer, {'workflow': WORKFLOW, 'event': 'workflow_dispatch',
                                   'ref': 'refs/heads/main', 'run_id': 123, 'run_attempt': 2})

    def test_wrong_repository_ref_event_sha_and_run_identity_fail_closed(self):
        event, environment = request()
        for key, value in (('GITHUB_REF', 'refs/heads/feature'), ('GITHUB_EVENT_NAME', 'push'),
                           ('GITHUB_REPOSITORY', 'other/X-Science'), ('GITHUB_REPOSITORY_ID', '1'),
                           ('GITHUB_SHA', 'b' * 40), ('GITHUB_RUN_ID', '0'),
                           ('GITHUB_RUN_ATTEMPT', 'bad'), ('GITHUB_SHA', 'echo injected')):
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                subject.validate_request(event, {**environment, key: value})
        for changed in ({}, {'repository': []}, {**event, 'inputs': []},
                        {**event, 'inputs': {'expected_sha': 'b' * 40}},
                        {**event, 'repository': {'id': True, 'full_name': subject.REPOSITORY}},
                        {**event, 'repository': {'id': subject.REPOSITORY_ID, 'full_name': 'other/X-Science'}}):
            with self.subTest(event=changed), self.assertRaises(ValueError):
                subject.validate_request(changed, environment)

    def test_unsafe_or_secret_paths_are_rejected(self):
        for value in ('', '.', '..', '../x', '/absolute', 'a//b', './index.html', 'a/../b',
                      'a\\b', 'C:drive', 'a\x00b', 'a\x7fb', '.env', 'assets/.secret',
                      'node_modules/a.js', 'assets/app.js.map', 'assets/code.ts', 'key.pem',
                      'task.sqlite', 'private.key', 'a' * 4097):
            with self.subTest(path=value), self.assertRaises(ValueError):
                subject.safe_path(value)
        self.assertEqual(str(subject.safe_path('assets/public.wasm')), 'assets/public.wasm')


class FrontendBuildArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name).resolve()
        self.repo = self.base / 'source'
        self.repo.mkdir()
        self.git_environment = {**os.environ, 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': os.devnull}
        self.run_git('init', '--quiet', '--template=')
        self.run_git('config', 'user.name', 'Fixture')
        self.run_git('config', 'user.email', 'fixture@example.invalid')
        self.run_git('config', 'commit.gpgsign', 'false')
        self.run_git('config', 'core.fsmonitor', 'false')
        (self.repo / '.gitignore').write_text('frontend/out/\n', encoding='utf-8')
        (self.repo / 'frontend').mkdir()
        (self.repo / 'frontend/package-lock.json').write_text('{"lockfileVersion":3}\n', encoding='utf-8')
        self.run_git('add', '.')
        self.run_git('commit', '--quiet', '-m', 'fixture source')
        self.sha = self.run_git('rev-parse', 'HEAD')
        self.event, self.environment = request(self.sha)
        self.renderer = self.repo / 'frontend/out/renderer'
        (self.renderer / 'assets').mkdir(parents=True)
        (self.renderer / 'index.html').write_bytes(b'<html>fixture build</html>')
        (self.renderer / 'assets/app.js').write_bytes(b'export const fixture = true;\r\n\x00\xff')
        self.output = self.base / 'artifact'

    def run_git(self, *arguments):
        return subprocess.check_output(['git', *arguments], cwd=self.repo,
                                       env=self.git_environment, text=True, timeout=30).strip()

    def produce(self):
        with patch.object(subject, 'tool_versions', return_value=TOOLS):
            return subject.produce(self.repo, self.output, self.event, self.environment)

    def test_real_archive_hashes_identity_inventory_and_normalized_members(self):
        manifest = self.produce()
        self.assertEqual(set(path.name for path in self.output.iterdir()), {subject.ARCHIVE_NAME, subject.MANIFEST_NAME})
        self.assertEqual(json.loads((self.output / subject.MANIFEST_NAME).read_text(encoding='utf-8')), manifest)
        self.assertEqual(manifest['source'], {'sha': self.sha, 'tree': self.run_git('rev-parse', 'HEAD^{tree}'),
                                            'lock_sha256': subject.digest(self.repo / 'frontend/package-lock.json')})
        self.assertEqual(manifest['archive']['sha256'], subject.digest(self.output / subject.ARCHIVE_NAME))
        self.assertEqual(manifest['archive']['bytes'], (self.output / subject.ARCHIVE_NAME).stat().st_size)
        self.assertEqual(manifest['archive']['file_count'], 2)
        self.assertEqual(manifest['archive']['total_bytes'], sum(item['bytes'] for item in manifest['files']))
        self.assertEqual(manifest['tools'], TOOLS)
        self.assertEqual(manifest['inputs']['commands'], subject.COMMANDS)
        with tarfile.open(self.output / subject.ARCHIVE_NAME, 'r:gz') as archive:
            self.assertEqual([member.name for member in archive.getmembers()],
                             ['renderer/' + entry['path'] for entry in manifest['files']])
            for member, entry in zip(archive.getmembers(), manifest['files']):
                self.assertTrue(member.isreg())
                self.assertEqual((member.mode, member.mtime, member.uid, member.gid), (0o644, 0, 0, 0))
                data = archive.extractfile(member).read()
                self.assertEqual((len(data), hashlib.sha256(data).hexdigest()), (entry['bytes'], entry['sha256']))
                self.assertEqual(data, (self.renderer / entry['path']).read_bytes())
        self.assertEqual(self.run_git('status', '--porcelain'), '')

    def test_long_unicode_path_uses_only_local_pax_path_metadata(self):
        relative = 'assets/' + 'a' * 120 + '-中文.js'
        (self.renderer / relative).write_bytes(b'public fixture')
        self.produce()
        with tarfile.open(self.output / subject.ARCHIVE_NAME) as archive:
            member = archive.getmember('renderer/' + relative)
            self.assertEqual(member.pax_headers, {'path': 'renderer/' + relative})

    def test_source_change_or_wrong_sha_cannot_produce(self):
        (self.repo / 'frontend/package-lock.json').write_text('changed', encoding='utf-8')
        with self.assertRaises(ValueError):
            self.produce()
        self.assertFalse(self.output.exists())

    def test_output_cannot_replace_existing_or_live_source_directories(self):
        with self.assertRaises(ValueError):
            subject.produce(self.repo, self.repo / 'artifact', self.event, self.environment)
        self.output.mkdir()
        marker = self.output / 'preserved'
        marker.write_bytes(b'owned fixture')
        with self.assertRaises(ValueError):
            self.produce()
        self.assertEqual(marker.read_bytes(), b'owned fixture')

    def test_hidden_and_secret_outputs_fail_instead_of_silent_filtering(self):
        for name in ('.env', 'assets/credentials.key', 'assets/source.ts', 'assets/app.js.map'):
            path = self.renderer / name
            path.write_bytes(b'not transportable')
            try:
                with self.subTest(path=name), self.assertRaises(ValueError):
                    self.produce()
                self.assertFalse(self.output.exists())
            finally:
                path.unlink()

    def test_hardlinked_output_is_rejected(self):
        os.link(self.renderer / 'index.html', self.renderer / 'assets/link.html')
        with self.assertRaises(ValueError):
            self.produce()
        self.assertFalse(self.output.exists())

    def test_symlink_output_is_rejected(self):
        try:
            (self.renderer / 'assets/link.js').symlink_to(self.renderer / 'index.html')
        except OSError as error:
            self.skipTest('Controlled symlink creation unavailable; OS error code ' + str(getattr(error, 'winerror', error.errno)))
        with self.assertRaises(ValueError):
            self.produce()

    @unittest.skipUnless(hasattr(os, 'mkfifo'), 'native FIFO fixture requires Unix')
    def test_special_output_is_rejected_without_opening_fifo(self):
        os.mkfifo(self.renderer / 'assets/special')
        with self.assertRaises(ValueError):
            self.produce()

    def test_portable_alias_and_missing_index_fail_closed(self):
        with patch.object(subject.unicodedata, 'normalize', return_value='same'):
            with self.assertRaisesRegex(ValueError, 'path alias'):
                self.produce()
        (self.renderer / 'index.html').unlink()
        with self.assertRaisesRegex(ValueError, 'nonempty index'):
            self.produce()

    def test_file_count_data_archive_and_manifest_bounds_are_real(self):
        for name, limit in (('MAX_FILES', 1), ('MAX_BYTES', 1)):
            with patch.object(subject, name, limit), self.subTest(bound=name), self.assertRaises(ValueError):
                self.produce()
            self.assertFalse(self.output.exists())
        with patch.object(subject, 'MAX_ARCHIVE_BYTES', 1), self.assertRaises(ValueError):
            self.produce()
        self.assertFalse((self.output / subject.MANIFEST_NAME).exists())
        with patch.object(subject, 'MAX_MANIFEST_BYTES', 1), self.assertRaises(ValueError):
            subject.write_manifest(self.base / 'bounded.json', {'schema': subject.SCHEMA})

    def test_file_changed_after_discovery_fails_before_reading_new_content(self):
        files, _ = subject.regular_files(self.repo)
        (self.renderer / 'assets/app.js').write_bytes(b'changed fixture output')
        with self.assertRaisesRegex(ValueError, 'changed after'):
            subject.archive_files(self.base / 'changed.tar.gz', files)

    def test_inaccessible_directory_is_not_silently_omitted(self):
        def denied_walk(root, *, followlinks, onerror):
            onerror(PermissionError('controlled unreadable directory'))
        with patch.object(subject.os, 'walk', side_effect=denied_walk), self.assertRaises(PermissionError):
            self.produce()
        self.assertFalse(self.output.exists())


class FrontendBuildWorkflowTests(unittest.TestCase):
    def test_build_only_workflow_has_canonical_registration(self):
        policy = json.loads((ROOT / 'docs/governance/github-actions-pins.json').read_text(encoding='utf-8'))
        self.assertIn(WORKFLOW, policy['allowed_workflows'])
        self.assertEqual(policy['workflow_bootstrap_jobs'][WORKFLOW], 'frontend-build')
        self.assertEqual(policy['workflow_checkout_contracts'][WORKFLOW],
                         {'ref': '${{ github.sha }}', 'fetch_depth': 1})
        self.assertTrue((ROOT / WORKFLOW).is_file())

    def test_dispatch_only_workflow_uses_original_build_checks_and_pinned_actions(self):
        content = (ROOT / WORKFLOW).read_text(encoding='utf-8')
        policy = json.loads((ROOT / 'docs/governance/github-actions-pins.json').read_text(encoding='utf-8'))
        self.assertIn('  workflow_dispatch:', content)
        for forbidden in ('  push:', '  pull_request:', '  release:', 'secrets.', 'continue-on-error:', '${{ inputs.'):
            self.assertNotIn(forbidden, content)
        uses = re.findall(r'uses: ([^@\s]+)@([0-9a-f]{40}) # (\S+)', content)
        self.assertEqual([name for name, _, _ in uses], ['actions/checkout', 'actions/setup-node', 'actions/upload-artifact'])
        for name, sha, version in uses:
            self.assertEqual((sha, version), (policy['actions'][name]['sha'], policy['actions'][name]['version']))
        ordered = ['frontend_build_artifact.py validate', 'npm ci --ignore-scripts --no-audit --no-fund',
                   'npm run build', 'npm run test:packaged', 'frontend_build_artifact.py package',
                   'uses: actions/upload-artifact@']
        offsets = [content.index(value) for value in ordered]
        self.assertEqual(offsets, sorted(offsets))
        self.assertIn('permissions:\n  contents: read', content)
        self.assertIn('retention-days: 7', content)
        self.assertIn('if-no-files-found: error', content)
        self.assertEqual(policy['required_aggregate_jobs'].keys(),
                         {'.github/workflows/quality-main.yml', '.github/workflows/quality-pr.yml'})

    def test_exact_tooling_ownership_does_not_broaden_runtime_or_hide_unknown_inputs(self):
        from scripts.quality import pr_fast_scope, verification_scope
        paths = [WORKFLOW, 'docs/governance/github-actions-pins.json',
                 'scripts/quality/verification_scope.json', 'scripts/packaging/frontend_build_artifact.py',
                 'scripts/packaging/test_frontend_build_artifact.py']
        groups = verification_scope.matched(ROOT, paths)
        self.assertEqual({group['name'] for group in groups},
                         {'workflow-contracts', 'verification-selection', 'frontend-build-artifact-producer'})
        self.assertIn(['python3', '-B', '-m', 'unittest', 'scripts.packaging.test_frontend_build_artifact'],
                      verification_scope.checks(groups))
        entries = [{'ImportPath': 'fixture/runtime', 'Dir': str(ROOT / 'internal/server')}]
        self.assertEqual(pr_fast_scope.select_packages(ROOT, paths, entries)[0], [])
        self.assertFalse(pr_fast_scope.frontend_affected(paths, ROOT))
        self.assertEqual(pr_fast_scope.select_packages(ROOT, ['scripts/unknown-frontend-input.py'], entries)[0],
                         ['fixture/runtime'])


if __name__ == '__main__':
    unittest.main()
