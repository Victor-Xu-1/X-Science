#!/usr/bin/env python3
"""Produce frontend-only transport evidence; never authorize deployment."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import stat
import subprocess
import tarfile
import unicodedata


REPOSITORY = 'Victor-Xu-1/X-Science'
REPOSITORY_ID = 1374130212
WORKFLOW = '.github/workflows/frontend-build-artifact.yml'
SCHEMA = 'x-science.frontend-build-artifact.v1'
ARCHIVE_NAME = 'frontend-build.tar.gz'
MANIFEST_NAME = 'frontend-build-manifest.json'
MAX_FILES, MAX_BYTES = 100000, 2 * 1024 ** 3
MAX_ARCHIVE_BYTES, MAX_MANIFEST_BYTES = 2 * 1024 ** 3, 64 * 1024 ** 2
COMMANDS = [['npm', 'ci', '--ignore-scripts', '--no-audit', '--no-fund'],
            ['npm', 'run', 'build'], ['npm', 'run', 'test:packaged']]
FORBIDDEN_SUFFIXES = {'.map', '.ts', '.tsx', '.go', '.py', '.pem', '.key', '.p12', '.db', '.sqlite', '.sqlite3', '.log'}


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def validate_request(event, environment):
    repository = event.get('repository', {}) if isinstance(event, dict) else {}
    inputs = event.get('inputs', {}) if isinstance(event, dict) else {}
    sha = environment.get('GITHUB_SHA', '')
    if (not isinstance(repository, dict) or not isinstance(inputs, dict)
            or type(repository.get('id')) is not int or repository['id'] != REPOSITORY_ID
            or repository.get('full_name') != REPOSITORY
            or environment.get('GITHUB_REPOSITORY') != REPOSITORY
            or environment.get('GITHUB_REPOSITORY_ID') != str(REPOSITORY_ID)
            or environment.get('GITHUB_EVENT_NAME') != 'workflow_dispatch'
            or environment.get('GITHUB_REF') != 'refs/heads/main'
            or not re.fullmatch('[0-9a-f]{40}', sha)
            or inputs.get('expected_sha') != sha):
        raise ValueError('frontend artifacts require this repository and exact dispatched main SHA')
    producer = {'workflow': WORKFLOW, 'event': 'workflow_dispatch', 'ref': 'refs/heads/main'}
    for field, name in (('run_id', 'GITHUB_RUN_ID'), ('run_attempt', 'GITHUB_RUN_ATTEMPT')):
        value = environment.get(name, '')
        if not re.fullmatch('[1-9][0-9]{0,19}', value):
            raise ValueError('frontend artifact producer requires immutable run coordinates')
        producer[field] = int(value)
    return sha, producer


def git(repo, *arguments):
    return subprocess.check_output(['git', '--no-optional-locks', *arguments], cwd=repo,
                                   text=True, timeout=30).strip()


def source_identity(repo, sha):
    lock = repo / 'frontend/package-lock.json'
    if (git(repo, 'rev-parse', 'HEAD') != sha or git(repo, 'status', '--porcelain')
            or lock.is_symlink() or not lock.is_file()):
        raise ValueError('frontend build source must be the clean exact checkout with regular lockfile')
    tree = git(repo, 'rev-parse', 'HEAD^{tree}')
    if not re.fullmatch('[0-9a-f]{40}', tree):
        raise ValueError('invalid frontend source tree')
    return {'sha': sha, 'tree': tree, 'lock_sha256': digest(lock)}


def safe_path(value):
    path = PurePosixPath(value)
    if (not value or value == '.' or len(value.encode('utf-8')) > 4096 or path.is_absolute()
            or path.as_posix() != value or any(ord(char) < 32 or ord(char) == 127 or char in '\\:' for char in value)
            or any(part.startswith('.') or part == 'node_modules' for part in path.parts)
            or path.suffix.lower() in FORBIDDEN_SUFFIXES):
        raise ValueError('frontend archive contains a forbidden path')
    return path


def fail_walk(error):
    raise error


def regular_files(repo):
    root = repo / 'frontend/out/renderer'
    if root.resolve() != root or not root.is_dir():
        raise ValueError('frontend output must be a real contained directory')
    files, total, seen = [], 0, set()
    for directory, folders, names in os.walk(root, followlinks=False, onerror=fail_walk):
        for name in folders + names:
            path = Path(directory) / name
            relative = path.relative_to(root).as_posix()
            safe_path(relative)
            alias = unicodedata.normalize('NFC', relative).casefold()
            if alias in seen:
                raise ValueError('frontend output contains a portable path alias')
            seen.add(alias)
            metadata = path.lstat()
            if name in folders:
                if not stat.S_ISDIR(metadata.st_mode):
                    raise ValueError('frontend output directory is linked or special')
                continue
            if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1:
                raise ValueError('frontend output must contain regular unlinked files only')
            total += metadata.st_size
            files.append((relative, path, metadata))
            if len(files) > MAX_FILES or total > MAX_BYTES:
                raise ValueError('frontend archive exceeds its bounded transport limits')
    if not any(name == 'index.html' and metadata.st_size > 0 for name, _, metadata in files):
        raise ValueError('frontend archive requires a nonempty index.html')
    return sorted(files), total


class HashingReader:
    def __init__(self, handle):
        self.handle, self.hash, self.bytes = handle, hashlib.sha256(), 0

    def read(self, size):
        block = self.handle.read(size)
        self.hash.update(block)
        self.bytes += len(block)
        return block


def archive_files(destination, files):
    entries = []
    with tarfile.open(destination, 'x:gz', format=tarfile.PAX_FORMAT) as archive:
        for relative, path, before in files:
            descriptor = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_BINARY', 0))
            with os.fdopen(descriptor, 'rb') as handle:
                current = os.fstat(handle.fileno())
                fields = ('st_dev', 'st_ino', 'st_size', 'st_mtime_ns', 'st_nlink')
                if (not stat.S_ISREG(current.st_mode) or current.st_nlink != 1
                        or any(getattr(current, key) != getattr(before, key) for key in fields)):
                    raise ValueError('frontend file changed after bounded discovery')
                member = tarfile.TarInfo('renderer/' + relative)
                member.size, member.mode, member.mtime = current.st_size, 0o644, 0
                reader = HashingReader(handle)
                archive.addfile(member, reader)
                after = os.fstat(handle.fileno())
                if reader.bytes != member.size or any(getattr(after, key) != getattr(current, key) for key in fields):
                    raise ValueError('frontend file changed during archive creation')
                entries.append({'path': relative, 'bytes': reader.bytes, 'sha256': reader.hash.hexdigest()})
    return entries


def tool_versions():
    result = {name: subprocess.check_output(command, text=True, timeout=30).strip()
              for name, command in (('node', ['node', '--version']), ('npm', ['npm', '--version']), ('git', ['git', '--version']))}
    return {**result, 'python': platform.python_version(), 'system': platform.system(), 'machine': platform.machine()}


def write_manifest(path, document):
    total = 1  # Final newline is included in the transport budget.
    with path.open('x', encoding='utf-8', newline='\n') as handle:
        for chunk in json.JSONEncoder(indent=2).iterencode(document):
            total += len(chunk.encode('utf-8'))
            if total > MAX_MANIFEST_BYTES:
                raise ValueError('frontend manifest exceeds its bounded transport limit')
            handle.write(chunk)
        handle.write('\n')


def produce(repo, output, event, environment):
    repo = repo.resolve(strict=True)
    output = output.resolve()
    if output == repo or repo in output.parents or output.exists():
        raise ValueError('artifact output must be a new directory outside source')
    sha, producer = validate_request(event, environment)
    source = source_identity(repo, sha)
    files, total = regular_files(repo)
    tools = tool_versions()
    output.mkdir(mode=0o700)
    archive = output / ARCHIVE_NAME
    entries = archive_files(archive, files)
    if archive.stat().st_size > MAX_ARCHIVE_BYTES:
        raise ValueError('frontend archive exceeds its compressed transport limit')
    if source_identity(repo, sha) != source:
        raise ValueError('frontend source identity changed during packaging')
    document = {'schema': SCHEMA, 'repository': {'id': REPOSITORY_ID, 'full_name': REPOSITORY},
                'source': source, 'producer': producer, 'inputs': {'commands': COMMANDS}, 'tools': tools,
                'archive': {'name': ARCHIVE_NAME, 'root': 'renderer', 'sha256': digest(archive),
                            'bytes': archive.stat().st_size, 'file_count': len(entries), 'total_bytes': total},
                'files': entries}
    write_manifest(output / MANIFEST_NAME, document)
    return document


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('validate', 'package'))
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if (args.output is None) == (args.mode == 'package'):
        parser.error('--output is required only for package mode')
    repo = Path(__file__).resolve().parents[2]
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text(encoding='utf-8'))
    if args.mode == 'validate':
        sha, _ = validate_request(event, os.environ)
        print(json.dumps(source_identity(repo, sha)))
    else:
        document = produce(repo, args.output, event, os.environ)
        print(json.dumps({'source': document['source'], 'archive': document['archive']}))


if __name__ == '__main__':
    main()
