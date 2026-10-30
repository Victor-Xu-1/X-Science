"""Pure inventory and receipt checks for an exact related-test run.

This module does not discover tests, invoke Vitest or authorize a CI result.
The caller must bind the inventory and input digest to the actual selection.
"""
from __future__ import annotations

from pathlib import PurePosixPath
import re

_SPEC_KEYS = frozenset(('project', 'path', 'pool'))
_BINDING_KEYS = frozenset(('head', 'base', 'inputs_sha256'))


def _binding(value: object) -> dict[str, str]:
    if not isinstance(value, dict) or set(value) != _BINDING_KEYS:
        raise ValueError('invalid execution binding')
    for field in ('head', 'base'):
        if not isinstance(value[field], str) or not re.fullmatch(r'(?:[0-9a-f]{40}|[0-9a-f]{64})', value[field]):
            raise ValueError('invalid revision')
    digest = value['inputs_sha256']
    if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
        raise ValueError('invalid input digest')
    return dict(value)


def _spec(value: object) -> dict[str, str]:
    if not isinstance(value, dict) or set(value) != _SPEC_KEYS:
        raise ValueError('invalid test specification')
    if any(not isinstance(value[k], str) or not value[k] or '\x00' in value[k] for k in _SPEC_KEYS):
        raise ValueError('invalid test specification field')
    path = value['path']
    parsed = PurePosixPath(path)
    if parsed.is_absolute() or '..' in parsed.parts or '\\' in path or parsed.as_posix() != path:
        raise ValueError('test path must be normalized and relative')
    return {key: value[key] for key in ('project', 'path', 'pool')}


def _key(value: dict[str, str]) -> tuple[bytes, bytes, bytes]:
    return tuple(value[k].encode('utf-8') for k in ('project', 'path', 'pool'))


def plan(binding: dict, specs: list[dict], *, max_shards: int) -> dict:
    binding = _binding(binding)
    if not isinstance(specs, list) or type(max_shards) is not int or max_shards < 1:
        raise ValueError('invalid partition inputs')
    normalized = sorted((_spec(item) for item in specs), key=_key)
    if len({_key(item) for item in normalized}) != len(normalized):
        raise ValueError('duplicate test specification')
    count = min(max_shards, len(normalized))
    shards = []
    if count:
        size, extra = divmod(len(normalized), count)
        offset = 0
        for index in range(count):
            end = offset + size + (index < extra)
            shards.append({'index': index, 'specs': normalized[offset:end]})
            offset = end
    return {'binding': binding, 'specs': normalized, 'shards': shards}


def verify(candidate: dict, binding: dict, reports: list[dict]) -> bool:
    binding = _binding(binding)
    if not isinstance(candidate, dict) or set(candidate) != {'binding', 'specs', 'shards'}:
        raise ValueError('invalid plan')
    shards = candidate['shards']
    if not isinstance(shards, list):
        raise ValueError('invalid shards')
    canonical = plan(binding, candidate['specs'], max_shards=max(1, len(shards)))
    if candidate != canonical:
        raise ValueError('plan identity or partition differs')
    if not isinstance(reports, list) or len(reports) != len(shards):
        raise ValueError('missing or unexpected shard report')
    seen = set()
    for report in reports:
        if not isinstance(report, dict) or set(report) != {
            'index', 'binding', 'exit_code', 'reason', 'unhandled_errors', 'queued', 'ended'
        }:
            raise ValueError('invalid shard report')
        index = report['index']
        if type(index) is not int or index < 0 or index >= len(shards) or index in seen:
            raise ValueError('duplicate or invalid shard')
        seen.add(index)
        if (_binding(report['binding']) != binding or type(report['exit_code']) is not int
                or report['exit_code'] != 0 or report['reason'] != 'passed'
                or type(report['unhandled_errors']) is not int or report['unhandled_errors'] != 0):
            raise ValueError('shard did not complete successfully')
        expected = {_key(spec) for spec in shards[index]['specs']}
        if not isinstance(report['queued'], list) or not isinstance(report['ended'], list):
            raise ValueError('invalid execution records')
        queued = [_key(_spec(item)) for item in report['queued']]
        ended = []
        for item in report['ended']:
            if not isinstance(item, dict) or set(item) != _SPEC_KEYS | {'state', 'ok'}:
                raise ValueError('invalid terminal record')
            if item['state'] not in ('passed', 'skipped') or item['ok'] is not True:
                raise ValueError('test module not successfully terminal')
            ended.append(_key(_spec({key: item[key] for key in _SPEC_KEYS})))
        if (len(queued) != len(expected) or len(ended) != len(expected)
                or len(set(queued)) != len(queued) or len(set(ended)) != len(ended)
                or set(queued) != expected or set(ended) != expected):
            raise ValueError('executed specifications differ from assigned shard')
    return True


def verify_native(candidate: dict, binding: dict, workers: list[dict]) -> bool:
    """Validate Vitest reporter observations with externally observed exits.

    Binding values must be independently computed and verified by the caller;
    a worker's own assertion is not proof of its source or dependency identity.
    """
    if not isinstance(candidate, dict) or not isinstance(candidate.get('specs'), list):
        raise ValueError('invalid plan')
    expected = sorted((_spec(item) for item in candidate['specs']), key=_key)
    if not isinstance(workers, list):
        raise ValueError('invalid worker reports')
    converted = []
    for worker in workers:
        if not isinstance(worker, dict) or set(worker) != {'index', 'binding', 'exit_code', 'report'}:
            raise ValueError('invalid worker report')
        report = worker['report']
        if not isinstance(report, dict) or set(report) != {
            'planned', 'queued', 'ended', 'reason', 'unhandled_errors'
        } or not isinstance(report['planned'], list):
            raise ValueError('invalid native reporter output')
        observed = sorted((_spec(item) for item in report['planned']), key=_key)
        if observed != expected:
            raise ValueError('worker resolved a different related inventory')
        converted.append({'index': worker['index'], 'binding': worker['binding'], 'exit_code': worker['exit_code'],
                          'reason': report['reason'], 'unhandled_errors': report['unhandled_errors'],
                          'queued': report['queued'], 'ended': report['ended']})
    return verify(candidate, binding, converted)
