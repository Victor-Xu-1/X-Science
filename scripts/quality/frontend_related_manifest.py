"""Produce local source/input evidence for a related-test inventory.

This does not attest a GitHub worker, verify an npm installation, validate a
Vitest resolved configuration or authorize CI promotion. The supervising
workflow must establish those independently before accepting this evidence.
"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess

_HEX = re.compile(r'(?:[0-9a-f]{40}|[0-9a-f]{64})\Z')


def _run(repo: Path, *arguments: str) -> bytes:
    result = subprocess.run(arguments, cwd=repo, capture_output=True, check=False, timeout=30)
    if result.returncode:
        raise ValueError('source or tool identity command failed')
    return result.stdout.strip()


def _regular_bytes(path: Path) -> bytes:
    if path.is_symlink() or not path.is_file():
        raise ValueError('required input is not a regular file')
    return path.read_bytes()


def _hash_regular(path: Path) -> str:
    return hashlib.sha256(_regular_bytes(path)).hexdigest()


def capture(repo: Path, *, head: str, base: str, inputs: list[str], node: Path,
            allow_generated_i18n: bool = False) -> dict:
    """Capture a clean checkout and selected inputs, refusing mismatches."""
    repo = repo.resolve(strict=True)
    if not all(isinstance(revision, str) and _HEX.fullmatch(revision) for revision in (head, base)):
        raise ValueError('expected immutable revisions')
    git_head = _run(repo, 'git', 'rev-parse', 'HEAD').decode('ascii')
    if git_head != head:
        raise ValueError('checkout is not exact execution commit')
    _require_source_state(repo, allow_generated_i18n)
    if set(base) != {'0'}:
        _run(repo, 'git', 'cat-file', '-e', base + '^{commit}')
    tree = _run(repo, 'git', 'rev-parse', 'HEAD^{tree}').decode('ascii')
    if not _HEX.fullmatch(tree) or not isinstance(inputs, list):
        raise ValueError('invalid source tree or selector inputs')
    normalized = []
    for value in inputs:
        if not isinstance(value, str) or not value or '\x00' in value or '\\' in value:
            raise ValueError('invalid related input')
        path = PurePosixPath(value)
        if path.is_absolute() or '..' in path.parts or path.as_posix() != value:
            raise ValueError('related input must be normalized and relative')
        normalized.append(value)
    if len(set(normalized)) != len(normalized):
        raise ValueError('duplicate related input')
    node = node.resolve(strict=True)
    frontend = repo / 'frontend'
    vitest_package = frontend / 'node_modules/vitest/package.json'
    vitest_package_bytes = _regular_bytes(vitest_package)
    vitest_package_sha256 = hashlib.sha256(vitest_package_bytes).hexdigest()
    vitest = json.loads(vitest_package_bytes)
    if not isinstance(vitest.get('version'), str):
        raise ValueError('Vitest version unavailable')
    record = {
        'head': head,
        'base': base,
        'tree': tree,
        'related_inputs': sorted(normalized, key=lambda value: value.encode('utf-8')),
        'package_lock_sha256': _hash_regular(frontend / 'package-lock.json'),
        'vitest_config_sha256': _hash_regular(frontend / 'vitest.config.ts'),
        'vitest_package_sha256': vitest_package_sha256,
        'vitest_version': vitest['version'],
        'node_binary_sha256': _hash_regular(node),
        'node_version': _run(repo, os.fspath(node), '--version').decode('utf-8'),
    }
    if allow_generated_i18n:
        record['generated_i18n_sha256'] = _hash_regular(repo / GENERATED_I18N)
    if (_run(repo, 'git', 'rev-parse', 'HEAD').decode('ascii') != head
            or _run(repo, 'git', 'rev-parse', 'HEAD^{tree}').decode('ascii') != tree):
        raise ValueError('checkout changed during capture')
    _require_source_state(repo, allow_generated_i18n)
    raw = json.dumps(record, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8')
    return {'record': record, 'binding': {'head': head, 'base': base, 'inputs_sha256': hashlib.sha256(raw).hexdigest()}}


def github_execution_identity(repo: Path, environment: dict[str, str], *, expected_job: str) -> dict:
    """Bind a PR worker to independently supplied GitHub event and checkout facts.

    Does not authenticate a host or permit promotion. The existing trusted
    workflow/required check owns execution and supplies this environment.
    """
    full_name = 'Victor-Xu-1/X-Science'
    if (environment.get('GITHUB_REPOSITORY_ID') != '1374130212'
            or environment.get('GITHUB_REPOSITORY') != full_name
            or environment.get('GITHUB_EVENT_NAME') != 'pull_request'
            or environment.get('GITHUB_JOB') != expected_job):
        raise ValueError('related shard is not the expected PR execution')
    for key in ('GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
        if not re.fullmatch(r'[1-9][0-9]*', environment.get(key, '')):
            raise ValueError('invalid GitHub run or attempt')
    for key in ('GITHUB_SHA', 'GITHUB_WORKFLOW_SHA'):
        if not re.fullmatch(r'[0-9a-f]{40}', environment.get(key, '')):
            raise ValueError('invalid GitHub execution commit')
    event_path = Path(environment.get('GITHUB_EVENT_PATH', ''))
    if not event_path.is_absolute() or not event_path.is_file() or event_path.is_symlink():
        raise ValueError('GitHub event must be an absolute regular file')
    if event_path.stat().st_size > 2 * 1024 * 1024:
        raise ValueError('GitHub event exceeds identity input budget')
    try:
        event = json.loads(event_path.read_bytes())
        repository = event['repository']
        pull = event['pull_request']
        number = event['number']
        base, pr_head = pull['base']['sha'], pull['head']['sha']
    except (KeyError, TypeError, ValueError) as error:
        raise ValueError('invalid GitHub PR event') from error
    if not isinstance(repository, dict) or not isinstance(pull, dict):
        raise ValueError('invalid repository or pull request identity')
    if (type(repository.get('id')) is not int or repository['id'] != 1374130212
            or repository.get('full_name') != full_name
            or type(number) is not int or number <= 0 or type(pull.get('number')) is not int
            or pull['number'] != number
            or not all(isinstance(value, str) and re.fullmatch(r'[0-9a-f]{40}', value)
                       for value in (base, pr_head))):
        raise ValueError('PR event repository or revision identity differs')
    ref = f'refs/pull/{number}/merge'
    if (environment.get('GITHUB_REF') != ref
            or environment.get('GITHUB_WORKFLOW_REF') !=
            f'{full_name}/.github/workflows/quality-pr.yml@{ref}'):
        raise ValueError('PR workflow or merge ref differs')
    execution = environment['GITHUB_SHA']
    if _run(repo, 'git', 'rev-parse', 'HEAD').decode('ascii') != execution:
        raise ValueError('checkout is not the actual workflow execution commit')
    parents = _run(repo, 'git', 'rev-list', '--parents', '-n', '1', 'HEAD').decode('ascii').split()
    if parents != [execution, base, pr_head]:
        raise ValueError('PR merge execution parents differ from the event')
    tree = _run(repo, 'git', 'rev-parse', 'HEAD^{tree}').decode('ascii')
    if not re.fullmatch(r'[0-9a-f]{40}', tree):
        raise ValueError('invalid execution tree')
    return {
        'repository_id': 1374130212, 'repository': full_name, 'pull_request': number,
        'event': 'pull_request', 'base': base, 'pr_head': pr_head,
        'execution_commit': execution, 'execution_tree': tree,
        'run_id': environment['GITHUB_RUN_ID'], 'run_attempt': environment['GITHUB_RUN_ATTEMPT'],
        'workflow_ref': environment['GITHUB_WORKFLOW_REF'],
        'workflow_commit': environment['GITHUB_WORKFLOW_SHA'],
    }


GENERATED_I18N = 'frontend/packages/desktop/src/renderer/services/i18n/i18n-keys.d.ts'


def _require_source_state(repo: Path, allow_generated_i18n: bool) -> None:
    if not allow_generated_i18n:
        if _run(repo, 'git', 'status', '--porcelain', '-z', '--untracked-files=all'):
            raise ValueError('checkout changed during source capture')
        return
    staged = _run(repo, 'git', 'diff', '--cached', '--name-only', '-z')
    untracked = _run(repo, 'git', 'ls-files', '--others', '--exclude-standard', '-z')
    changed = _run(repo, 'git', 'diff', '--name-only', '-z').split(b'\0')
    if staged or untracked or any(path and path.decode('utf-8') != GENERATED_I18N for path in changed):
        raise ValueError('only the original generated i18n declaration may differ')


def execution_binding(source: dict, context: dict, runtime: dict) -> dict:
    """Bind independently captured worker inputs, not an artifact assertion."""
    if (not isinstance(source, dict) or not isinstance(context, dict) or not isinstance(runtime, dict)
            or source.get('head') != context.get('execution_commit')
            or source.get('base') != context.get('base')
            or source.get('tree') != context.get('execution_tree')):
        raise ValueError('source capture differs from actual CI execution')
    if not all(isinstance(source.get(key), str) and re.fullmatch(r'[0-9a-f]{40}', source[key])
               for key in ('head', 'base', 'tree')):
        raise ValueError('invalid source revision identity')
    try:
        raw = json.dumps({'source': source, 'context': context, 'runtime': runtime},
                         ensure_ascii=False, sort_keys=True, separators=(',', ':'),
                         allow_nan=False).encode('utf-8')
    except (TypeError, ValueError) as error:
        raise ValueError('noncanonical runtime binding') from error
    return {'head': source['head'], 'base': source['base'],
            'inputs_sha256': hashlib.sha256(raw).hexdigest()}
