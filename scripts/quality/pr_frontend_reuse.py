"""Verify one original PR related-test execution at the main related-test slot.

The token-bearing fetch command only reads trusted evidence. The ordinary main
frontend step seals that input by its preceding step's digest, independently
captures its own runtime, and either references the complete original execution
or runs the unchanged related command. It never relabels PR tests as main tests.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.quality import frontend_related_ci as related
from scripts.quality import frontend_related_manifest as identity
from scripts.quality import frontend_related_partition as partition
from scripts.quality import pr_go_reuse, pr_reuse_github

ORIGIN_SCHEMA = 'x-science.frontend-pr-origin.v1'
VERDICT_SCHEMA = 'x-science.main-related-reuse.v1'
BUDGET_SECONDS = 25
CAN_REAP_PROCESS_GROUP = os.name == 'posix' and hasattr(os, 'killpg')


def digest(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def decode(raw: bytes):
    def unique(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError('duplicate original evidence key')
            value[key] = item
        return value
    return json.loads(raw, object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite original evidence')))


def bounded_json_bytes(path: Path) -> bytes:
    if path.is_symlink() or not path.is_file():
        raise ValueError('origin JSON is not a regular file')
    with path.open('rb') as stream:
        raw = stream.read(related.MAX_JSON + 1)
    if len(raw) > related.MAX_JSON:
        raise ValueError('origin JSON exceeds its bound')
    return raw


def git(repo: Path, *args: str) -> str:
    return identity._run(repo, 'git', *args).decode('utf-8')


def main_identity(repo: Path, base: str, head: str) -> dict:
    """Establish a real main push without weakening the PR producer identity."""
    env = os.environ
    if (env.get('GITHUB_ACTIONS') != 'true' or env.get('GITHUB_EVENT_NAME') != 'push'
            or env.get('GITHUB_REF') != 'refs/heads/main'
            or env.get('GITHUB_REPOSITORY') != 'Victor-Xu-1/X-Science'
            or env.get('GITHUB_REPOSITORY_ID') != '1374130212'
            or env.get('GITHUB_JOB') != 'main-frontend-tests'
            or env.get('GITHUB_SHA') != head
            or env.get('GITHUB_WORKFLOW_REF') != 'Victor-Xu-1/X-Science/.github/workflows/quality-main.yml@refs/heads/main'):
        raise ValueError('not the exact main frontend execution')
    if not all(re.fullmatch('[0-9a-f]{40}', value or '') for value in (base, head, env.get('GITHUB_WORKFLOW_SHA'))):
        raise ValueError('invalid main revision')
    if not all(re.fullmatch('[1-9][0-9]*', env.get(key, '')) for key in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT')):
        raise ValueError('invalid main run identity')
    event_path = Path(env.get('GITHUB_EVENT_PATH', ''))
    if not event_path.is_absolute():
        raise ValueError('main event is not an absolute file')
    event = related.read_json(event_path)
    if (event.get('before') != base or event.get('after') != head
            or event.get('repository', {}).get('id') != 1374130212
            or event.get('repository', {}).get('full_name') != env['GITHUB_REPOSITORY']
            or git(repo, 'rev-parse', 'HEAD') != head or git(repo, 'rev-parse', 'HEAD^') != base):
        raise ValueError('main source differs from its actual push')
    return {'repository': env['GITHUB_REPOSITORY'], 'repository_id': 1374130212,
            'event': 'push', 'base': base, 'execution_commit': head,
            'execution_tree': git(repo, 'rev-parse', 'HEAD^{tree}'),
            'run_id': env['GITHUB_RUN_ID'], 'run_attempt': env['GITHUB_RUN_ATTEMPT'],
            'workflow_ref': env['GITHUB_WORKFLOW_REF'], 'workflow_commit': env['GITHUB_WORKFLOW_SHA']}


def unchanged_ci_authority(repo: Path, base: str, head: str) -> None:
    paths = git(repo, 'diff', '--name-only', base, head).splitlines()
    if any(path.startswith(('.github/', 'scripts/quality/')) for path in paths):
        raise ValueError('CI authority changed')


def fetch_origin(repo: Path, base: str, head: str, directory: Path, token: str | None) -> Path:
    """Pure network/Git reader: no npm, Node, tests, configuration execution."""
    output = related.external_directory(repo, directory, create=True)
    result = {'schema': ORIGIN_SCHEMA, 'status': 'fallback', 'reason': 'origin_unavailable'}
    stage = 'main_identity'
    try:
        current = main_identity(repo, base, head)
        stage = 'ci_authority_or_scope'
        unchanged_ci_authority(repo, base, head)
        from scripts.quality.pr_fast_scope import frontend_affected
        if not frontend_affected(git(repo, 'diff', '--name-only', base, head).splitlines(), repo):
            raise ValueError('no frontend input affected')
        if git(repo, 'status', '--porcelain', '--untracked-files=all'):
            raise ValueError('source changed before fetch')
        deadline = time.monotonic() + BUDGET_SECONDS
        stage = 'trusted_PR_origin'
        receipts = pr_go_reuse._bounded(lambda: pr_reuse_github.fetch_merged_pr_receipts(
            current['repository'], head, base, token, deadline=deadline,
            profile=pr_reuse_github.FRONTEND_PROFILE), BUDGET_SECONDS)
        if current != main_identity(repo, base, head) or git(repo, 'status', '--porcelain', '--untracked-files=all'):
            raise ValueError('main source changed while fetching')
        artifacts = []
        for artifact in receipts['artifacts']:
            destination = output / ('artifact-' + str(artifact['id']))
            destination.mkdir(exist_ok=False)
            files = artifact['files']
            for name, raw in files.items():
                with (destination / name).open('xb') as stream:
                    stream.write(raw)
            artifacts.append({**{key: value for key, value in artifact.items() if key != 'files'},
                              'files': {name: digest(raw) for name, raw in files.items()}})
        result = {'schema': ORIGIN_SCHEMA, 'status': 'fetched', 'main_context': current,
                  'receipts': {**receipts, 'artifacts': artifacts}}
    except Exception:
        # No response bodies, URLs, environment, token or exception detail.
        print('Frontend origin unavailable; the original related command remains required.', flush=True)
        result['reason'] = stage
    target = output / 'origin.json'
    related.write_json(target, result)
    return target


def load_origin(repo: Path, directory: Path, expected_digest: str, current: dict) -> dict:
    """A workflow-step output seals the fetch before npm/config code executes."""
    output = related.external_directory(repo, directory)
    path = output / 'origin.json'
    raw = bounded_json_bytes(path)
    if not re.fullmatch('[0-9a-f]{64}', expected_digest or '') or digest(raw) != expected_digest:
        raise ValueError('origin seal differs from pure fetch step')
    # Authenticate and decode the same bytes, never a second mutable file read.
    record = decode(raw)
    if (set(record) != {'schema', 'status', 'main_context', 'receipts'}
            or record['schema'] != ORIGIN_SCHEMA or record['status'] != 'fetched'
            or record['main_context'] != current):
        raise ValueError('origin is not this main execution')
    receipts = record['receipts']
    artifacts = []
    for artifact in receipts['artifacts']:
        files = {}
        for name, expected in artifact['files'].items():
            # File names were closed by the authenticated archive profile. Keep
            # the local boundary closed as well, even before reading a member.
            if not re.fullmatch(r'(?:plan|pending-plan|discovery|discovery-inputs|worker-[0-3])\.json', name):
                raise ValueError('unexpected origin member')
            member = output / ('artifact-' + str(artifact['id'])) / name
            if member.is_symlink() or member.parent.is_symlink() or not member.is_file() or member.stat().st_size > related.MAX_JSON:
                raise ValueError('origin member unavailable')
            raw = bounded_json_bytes(member)
            if digest(raw) != expected:
                raise ValueError('origin member changed')
            # The same strict JSON parser rejects duplicate/nonfinite members.
            decode(raw)
            files[name] = raw
        artifacts.append({**artifact, 'files': files})
    return {**receipts, 'artifacts': artifacts}


def validate_equivalence(receipts: dict, current: dict, source: dict, runtime: dict,
                         specs: list[dict], command: list[str]) -> dict:
    """Pure verification: original binding and current capture remain distinct."""
    artifacts = receipts['artifacts']
    plans = [item for item in artifacts if item['index'] is None]
    workers = [item for item in artifacts if item['index'] is not None]
    if len(plans) != 1:
        raise ValueError('one original plan is required')
    bundle = decode(plans[0]['files']['plan.json'])
    execution = bundle['context']
    expected_context = {'repository_id', 'repository', 'pull_request', 'event', 'base', 'pr_head',
                        'execution_commit', 'execution_tree', 'run_id', 'run_attempt', 'workflow_ref', 'workflow_commit'}
    if set(execution) != expected_context:
        raise ValueError('unknown original context')
    run, pr = receipts['run'], receipts['pr']
    if (receipts['repository'] != {'full_name': current['repository'], 'id': current['repository_id']}
            or pr['base_sha'] != current['base'] or pr['merge_commit_sha'] != current['execution_commit']
            or run['execution_tree'] != current['execution_tree'] or run['head_sha'] != pr['head_sha']
            or execution['repository_id'] != current['repository_id'] or execution['repository'] != current['repository']
            or execution['pull_request'] != pr['number'] or execution['event'] != 'pull_request'
            or execution['base'] != current['base'] or execution['pr_head'] != pr['head_sha']
            or execution['execution_commit'] != run['execution_sha'] or execution['execution_tree'] != current['execution_tree']
            or execution['run_id'] != str(run['id']) or execution['run_attempt'] != str(run['attempt'])
            or execution['workflow_ref'] != current['repository'] + '/.github/workflows/quality-pr.yml@refs/pull/' + str(pr['number']) + '/merge'
            or not re.fullmatch('[0-9a-f]{40}', execution['workflow_commit'])):
        raise ValueError('original context differs from authenticated run')
    related.validate_bundle(bundle, execution)
    if bundle['mode'] != 'sharded' or bundle['runtime']['command'] != command:
        raise ValueError('unsupported original related execution')
    source_fields = {'head', 'base', 'tree', 'related_inputs', 'package_lock_sha256', 'vitest_config_sha256',
                     'vitest_package_sha256', 'vitest_version', 'node_binary_sha256', 'node_version', 'generated_i18n_sha256'}
    runtime_fields = {'command', 'configuration', 'npm_version', 'installed_lock_sha256', 'vite_package_sha256'}
    if set(source) != source_fields or set(runtime) != runtime_fields:
        raise ValueError('unknown or incomplete current capture')
    plan_files = plans[0]['files']
    discovery = decode(plan_files['discovery.json'])
    pending = decode(plan_files['pending-plan.json'])
    if (decode(plan_files['discovery-inputs.json']) != command[9:]
            or pending != {key: value for key, value in bundle.items() if key != 'prepared'}
            or discovery.get('eligible') is not True
            or discovery.get('configuration') != bundle['runtime']['configuration']
            or partition.plan(bundle['plan']['binding'], discovery.get('specs'), max_shards=related.MAX_SHARDS) != bundle['plan']):
        raise ValueError('original preparation capture differs from sealed plan')
    if source != {**bundle['source'], 'head': current['execution_commit']} or runtime != bundle['runtime']:
        raise ValueError('main independently captured different inputs or configuration')
    resolved = partition.plan(identity.execution_binding(bundle['source'], execution, runtime), specs,
                              max_shards=related.MAX_SHARDS)
    if resolved != bundle['plan']:
        raise ValueError('complete main inventory differs from original plan')
    if len(workers) != len(bundle['plan']['shards']) or {item['index'] for item in workers} != set(range(len(workers))):
        raise ValueError('missing or duplicate original worker')
    expected_names = {f"pr-frontend-related-plan-{run['id']}-{run['attempt']}"} | {
        f"pr-frontend-related-result-{run['id']}-{run['attempt']}-{index}" for index in range(len(workers))}
    if (len(artifacts) != len(expected_names) or {item['name'] for item in artifacts} != expected_names
            or len({item['id'] for item in artifacts}) != len(artifacts)
            or any(type(item['id']) is not int or item['id'] <= 0 for item in artifacts)
            or any(type(item['index']) is not int for item in workers)):
        raise ValueError('original artifacts are not the exact same-attempt set')
    reports = [decode(item['files']['worker-' + str(item['index']) + '.json']) for item in workers]
    partition.verify_native(bundle['plan'], bundle['plan']['binding'], reports)
    return {'schema': VERDICT_SCHEMA, 'status': 'verified-reuse', 'main_context': current,
            'origin_execution_context': execution, 'main_source': source,
            'origin_plan_sha256': digest(plans[0]['files']['plan.json']),
            'origin_artifacts': [{key: item[key] for key in ('id', 'name', 'index', 'digest')} for item in artifacts],
            'origin_checks': receipts['checks'], 'origin_jobs': receipts['jobs'],
            'tests_not_reexecuted': True, 'scope': 'original related Vitest command only',
            'identity_relation': 'same tree/base and exact inputs; PR execution SHA is never relabelled main'}


def capture_current(repo: Path, current: dict, command: list[str], output: Path) -> dict:
    before_source, before_runtime, _ = related.capture(repo, current, command, {})
    selected = related.discover(repo, command, output / 'current-discovery.json')
    after_source, after_runtime, _ = related.capture(repo, current, command, {})
    if before_source != after_source or before_runtime != after_runtime or selected.get('eligible') is not True:
        raise ValueError('current discovery is unsupported or changed inputs')
    source, runtime, binding = related.capture(repo, current, command, selected['configuration'])
    return {'source': source, 'runtime': runtime, 'binding': binding, 'specs': selected['specs']}


def bounded_capture(repo: Path, current: dict, command: list[str], output: Path) -> dict:
    """Reuse the original capture/discovery inside one owned, no-token process.

    A synchronous timeout reaps this process group before ordinary tests run;
    no late optional probe can write an acceptance verdict or compete with them.
    """
    request = output / 'current-request.json'
    related.write_json(request, {'context': current, 'command': command})
    environment = {key: value for key, value in os.environ.items() if key != 'GITHUB_TOKEN'}
    if not CAN_REAP_PROCESS_GROUP:
        raise ValueError('bounded discovery requires the existing hosted POSIX runner')
    child = subprocess.Popen([sys.executable, '-B', str(Path(__file__).resolve()), 'capture', '--repo', str(repo),
                              '--directory', str(output)], cwd=repo, env=environment,
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        code = child.wait(timeout=BUDGET_SECONDS)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()
        raise ValueError('current capture deadline elapsed') from None
    if code:
        raise ValueError('current capture failed')
    return related.read_json(output / 'current-capture.json')


def try_reuse(repo: Path, base: str, head: str, command: list[str], directory: Path,
              origin_digest: str) -> bool:
    if os.getenv('GITHUB_ACTIONS') != 'true' or os.getenv('GITHUB_EVENT_NAME') != 'push':
        return False
    stage = 'main_identity'
    try:
        current = main_identity(repo, base, head)
        unchanged_ci_authority(repo, base, head)
        stage = 'sealed_origin'
        receipts = load_origin(repo, directory, origin_digest, current)
        output = related.external_directory(repo, directory)
        stage = 'current_capture'
        capture = bounded_capture(repo, current, command, output)
        stage = 'scope_equivalence'
        result = validate_equivalence(receipts, current, capture['source'], capture['runtime'], capture['specs'], command)
        stage = 'source_unchanged'
        if current != main_identity(repo, base, head):
            raise ValueError('current main identity changed')
        identity._require_source_state(repo, True)
        stage = 'verdict_write'
        related.write_json(output / 'verified-reuse.json', result)
        print('Main related verification references the original complete PR execution; tests_not_reexecuted=true.', flush=True)
        return True
    except Exception:
        try:
            related.write_json(related.external_directory(repo, directory) / 'reuse-decision.json',
                               {'schema': VERDICT_SCHEMA, 'status': 'fallback', 'reason': stage})
        except Exception:
            pass
        print('Main related evidence unavailable; executing the original related command.', flush=True)
        return False


def main() -> int:
    token = os.environ.pop('GITHUB_TOKEN', None)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['fetch', 'capture'])
    parser.add_argument('--repo', type=Path, default=Path.cwd())
    parser.add_argument('--base')
    parser.add_argument('--head')
    parser.add_argument('--directory', required=True, type=Path)
    args = parser.parse_args()
    repo = args.repo.resolve()
    if args.mode == 'fetch':
        try:
            receipt = fetch_origin(repo, args.base, args.head, args.directory, token)
            output = os.environ.get('GITHUB_OUTPUT')
            if output:
                with Path(output).open('a', encoding='utf-8') as stream:
                    stream.write('receipt-sha256=' + related.digest(receipt) + '\n')
        except Exception:
            # A failed optional read must not replace ordinary frontend tests.
            print('Frontend origin seal unavailable; ordinary related tests remain required.', flush=True)
    else:
        directory = related.external_directory(repo, args.directory)
        request = related.read_json(directory / 'current-request.json')
        expected = request['context']
        if expected != main_identity(repo, expected['base'], expected['execution_commit']):
            raise ValueError('capture is not the same main execution')
        result = capture_current(repo, expected, request['command'], directory)
        related.write_json(directory / 'current-capture.json', result)
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception:
        print('Frontend evidence preparation unavailable; no related tests were accepted.', file=sys.stderr)
        raise SystemExit(1)
