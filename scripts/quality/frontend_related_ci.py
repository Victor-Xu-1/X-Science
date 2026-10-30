"""Existing frontend selector's optional, exact related-shard workflow adapter.

Unsupported configuration retains the original command. This does not own
branch protection, accept another run's receipts or replace any static/build gate.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.quality import frontend_related_manifest as identity
from scripts.quality import frontend_related_partition as partition

SCHEMA = 'x-science.frontend-related-ci.v1'
MAX_SHARDS = 4
MAX_JSON = 16 * 1024 * 1024


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_json(path: Path) -> dict:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_JSON:
        raise ValueError('missing or oversized related evidence')
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('duplicate JSON evidence key')
            result[key] = value
        return result
    return json.loads(path.read_bytes(), object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError('invalid JSON number')))


def write_json(path: Path, value: dict | list) -> None:
    with path.open('x', encoding='utf-8') as stream:
        json.dump(value, stream, ensure_ascii=False, sort_keys=True, allow_nan=False)


def external_directory(repo: Path, path: Path, *, create: bool = False) -> Path:
    path = path.resolve()
    if path == repo or repo in path.parents:
        raise ValueError('related evidence must be outside source')
    if create:
        path.mkdir(parents=True, exist_ok=False)
    if not path.is_dir():
        raise ValueError('related evidence directory unavailable')
    return path


def context(repo: Path, job: str) -> dict:
    return identity.github_execution_identity(repo, dict(os.environ), expected_job=job)


def capture(repo: Path, execution: dict, command: list[str], configuration: dict) -> tuple[dict, dict, dict]:
    node = Path(shutil.which('node') or '').resolve(strict=True)
    source = identity.capture(repo, head=execution['execution_commit'], base=execution['base'],
                              inputs=command[9:], node=node, allow_generated_i18n=True)['record']
    front = repo / 'frontend'
    runtime = {
        'command': command, 'configuration': configuration,
        'npm_version': identity._run(repo, 'npm', '--version').decode('utf-8'),
        'installed_lock_sha256': digest(front / 'node_modules/.package-lock.json'),
        'vite_package_sha256': digest(front / 'node_modules/vite/package.json'),
    }
    return source, runtime, identity.execution_binding(source, execution, runtime)


def discover(repo: Path, command: list[str], output: Path) -> dict:
    inputs = output.with_name(output.stem + '-inputs.json')
    write_json(inputs, command[9:])
    run = subprocess.run(
        ['node', '--max-old-space-size=2048', str(repo / 'scripts/quality/relatedShardDiscover.mjs'),
         str(repo / 'frontend'), str(inputs), str(output)], cwd=repo / 'frontend', check=False)
    if run.returncode:
        return {'eligible': False}
    return read_json(output)


def defer_or_run(repo: Path, base: str, head: str, command: list[str], directory: Path) -> int:
    """Called only at the original selector's related command position."""
    output = external_directory(repo, directory)
    execution = context(repo, 'pr-frontend-plan')
    if (base, head) != (execution['base'], execution['execution_commit']):
        raise ValueError('selector revisions differ from actual PR execution')
    initial = context(repo, 'pr-frontend-plan')
    before_source, before_runtime, _ = capture(repo, execution, command, {})
    selected = discover(repo, command, output / 'discovery.json')
    after_source, after_runtime, _ = capture(repo, execution, command, {})
    if before_source != after_source or before_runtime != after_runtime:
        raise ValueError('discovery changed original source or toolchain inputs')
    candidate = {'schema': SCHEMA, 'context': execution, 'mode': 'serial'}
    if selected.get('eligible') is True and len(selected.get('specs', [])) >= 2:
        source, runtime, binding = capture(repo, execution, command, selected['configuration'])
        candidate.update(mode='sharded', source=source, runtime=runtime,
                         plan=partition.plan(binding, selected['specs'], max_shards=MAX_SHARDS))
        code = 0
    else:
        code = subprocess.run(command, cwd=repo / 'frontend', check=False).returncode
    if context(repo, 'pr-frontend-plan') != initial:
        raise ValueError('CI execution changed during related preparation')
    if code == 0:
        write_json(output / 'pending-plan.json', candidate)
    return code


def prepare(repo: Path, output: Path) -> None:
    execution = context(repo, 'pr-frontend-plan')
    output = external_directory(repo, output, create=True)
    result = subprocess.run(
        [sys.executable, '-B', str(repo / 'scripts/quality/pr_fast_scope.py'), '--repo', str(repo),
         '--frontend', '--base', execution['base'], '--head', execution['execution_commit'],
         '--related-plan-dir', str(output)], cwd=repo, check=False)
    if result.returncode:
        raise ValueError('original frontend checks failed')
    if context(repo, 'pr-frontend-plan') != execution:
        raise ValueError('preparation execution identity changed')
    candidate = (read_json(output / 'pending-plan.json') if (output / 'pending-plan.json').exists()
                 else {'schema': SCHEMA, 'context': execution, 'mode': 'serial'})
    if candidate.get('context') != execution:
        raise ValueError('preparation produced wrong execution identity')
    if candidate['mode'] == 'sharded':
        source, runtime, binding = capture(repo, execution, candidate['runtime']['command'],
                                           candidate['runtime']['configuration'])
        if (source != candidate['source'] or runtime != candidate['runtime']
                or binding != candidate['plan']['binding']):
            raise ValueError('source or toolchain changed during original build/static checks')
    candidate['prepared'] = True
    write_json(output / 'plan.json', candidate)
    count = len(candidate['plan']['shards']) if candidate['mode'] == 'sharded' else 1
    version = identity._run(repo, 'node', '--version').decode('ascii').removeprefix('v')
    with Path(os.environ['GITHUB_OUTPUT']).open('a', encoding='utf-8') as stream:
        stream.write('mode=' + candidate['mode'] + '\n')
        stream.write('node-version=' + version + '\n')
        stream.write('plan-sha256=' + digest(output / 'plan.json') + '\n')
        stream.write('matrix=' + json.dumps({'include': [{'index': i, 'count': count}
                                                          for i in range(count)]}) + '\n')


def validate_bundle(bundle: dict, execution: dict) -> None:
    if (not isinstance(bundle, dict) or bundle.get('schema') != SCHEMA
            or bundle.get('context') != execution or bundle.get('prepared') is not True
            or bundle.get('mode') not in ('serial', 'sharded')):
        raise ValueError('plan was not prepared by this exact workflow execution')
    expected_keys = {'schema', 'context', 'mode', 'prepared'}
    if bundle['mode'] == 'sharded':
        expected_keys |= {'source', 'runtime', 'plan'}
    if set(bundle) != expected_keys:
        raise ValueError('unexpected plan evidence fields')
    if bundle['mode'] == 'sharded':
        plan = bundle['plan']
        if not 1 < len(plan['shards']) <= MAX_SHARDS:
            raise ValueError('invalid related worker count')
        binding = identity.execution_binding(bundle['source'], execution, bundle['runtime'])
        canonical = partition.plan(binding, plan['specs'], max_shards=MAX_SHARDS)
        if canonical != plan:
            raise ValueError('plan binding or partition is noncanonical')


def load_plan(path: Path, expected_digest: str, execution: dict) -> dict:
    if len(expected_digest) != 64 or digest(path) != expected_digest:
        raise ValueError('downloaded plan differs from preparation output')
    bundle = read_json(path)
    validate_bundle(bundle, execution)
    return bundle


def worker(repo: Path, plan_path: Path, plan_digest: str, output: Path, index: int) -> None:
    execution = context(repo, 'pr-frontend-related-shards')
    bundle = load_plan(plan_path, plan_digest, execution)
    output = external_directory(repo, output, create=True)
    if bundle['mode'] != 'sharded' or not 0 <= index < len(bundle['plan']['shards']):
        raise ValueError('worker is not assigned by this plan')
    from scripts.quality import pr_fast_scope as scope
    paths = scope.changed_paths(repo, execution['base'], execution['execution_commit'])
    behavior, _, _ = scope.behavior_scope(repo, execution['base'], execution['execution_commit'], paths)
    projections = scope.pr_metadata_scope.version_projection_paths(
        repo, execution['base'], execution['execution_commit'])
    inputs = [path for path in behavior if path not in projections and not scope.documentation_only(path)]
    command = scope.pr_frontend_scope.related_command(inputs)
    if command != bundle['runtime']['command']:
        raise ValueError('worker selector differs from original related selection')
    selected = discover(repo, command, output / 'discovery.json')
    if selected.get('eligible') is not True:
        raise ValueError('worker cannot prove original configuration')
    source, runtime, binding = capture(repo, execution, command, selected['configuration'])
    if (source != bundle['source'] or runtime != bundle['runtime']
            or binding != bundle['plan']['binding']
            or partition.plan(binding, selected['specs'], max_shards=MAX_SHARDS) != bundle['plan']):
        raise ValueError('worker independently resolved different inputs/configuration/specifications')
    native = output / 'native-report.json'
    wrapper = output / 'shard.config.mjs'
    with wrapper.open('x', encoding='utf-8', newline='\n') as stream:
        stream.write(selected['wrapper_source'])
    count = len(bundle['plan']['shards'])
    environment = {**os.environ, 'X_SCIENCE_RELATED_REPORT': str(native),
                   'X_SCIENCE_RELATED_INDEX': str(index + 1), 'X_SCIENCE_RELATED_COUNT': str(count),
                   'X_SCIENCE_RELATED_CONFIG': str(wrapper)}
    command = [*command, '--config=' + str(wrapper), '--shard=' + str(index + 1) + '/' + str(count),
               *['--reporter=' + name for name in selected['configuration']['default_reporters']],
               '--reporter=' + str(repo / 'scripts/quality/relatedShardReporter.mjs')]
    result = subprocess.run(command, cwd=repo / 'frontend', env=environment, check=False)
    after_source, after_runtime, after_binding = capture(
        repo, context(repo, 'pr-frontend-related-shards'), bundle['runtime']['command'], selected['configuration'])
    if (after_source != source or after_runtime != runtime or after_binding != binding):
        raise ValueError('worker inputs changed during actual execution')
    observed = read_json(native)
    if observed.pop('configuration_sha256', None) != selected['configuration']['sha256']:
        raise ValueError('actual worker effective configuration differs from discovery')
    report = {'index': index, 'binding': binding, 'exit_code': result.returncode, 'report': observed}
    write_json(output / f'worker-{index}.json', report)
    if result.returncode:
        raise ValueError('actual related shard failed')


def aggregate(repo: Path, plan_path: Path, expected_digest: str, reports: Path,
              preparation_result: str, shard_result: str) -> None:
    execution = context(repo, 'pr-frontend-tests')
    bundle = load_plan(plan_path, expected_digest, execution)
    if preparation_result != 'success':
        raise ValueError('preparation and original static/build checks did not succeed')
    if bundle['mode'] == 'serial':
        if shard_result != 'skipped':
            raise ValueError('unsupported execution did not use original serial branch')
        return
    if shard_result != 'success':
        raise ValueError('a required related worker failed, was cancelled or is missing')
    files = list(reports.rglob('worker-*.json'))
    workers = [read_json(path) for path in files]
    partition.verify_native(bundle['plan'], bundle['plan']['binding'], workers)


def main() -> int:
    # Match the original selector: discovery and tests never inherit its API token.
    os.environ.pop('GITHUB_TOKEN', None)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['prepare', 'worker', 'aggregate'])
    parser.add_argument('--repo', type=Path, default=Path.cwd())
    parser.add_argument('--directory', type=Path, required=True)
    parser.add_argument('--plan', type=Path)
    parser.add_argument('--plan-digest')
    parser.add_argument('--index', type=int)
    parser.add_argument('--preparation-result')
    parser.add_argument('--shard-result')
    args = parser.parse_args()
    repo = args.repo.resolve()
    if args.mode == 'prepare':
        prepare(repo, args.directory)
    elif args.mode == 'worker':
        worker(repo, args.plan, args.plan_digest, args.directory, args.index)
    else:
        aggregate(repo, args.plan, args.plan_digest, args.directory,
                  args.preparation_result, args.shard_result)
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print('Related frontend execution rejected: ' + str(error), file=sys.stderr)
        raise SystemExit(1)
