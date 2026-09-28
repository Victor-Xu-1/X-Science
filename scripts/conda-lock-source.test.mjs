import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { readLockedRecords } from './conda-lock-source.mjs';

const generator = fileURLToPath(new URL('./generate-conda-runtime-lock.mjs', import.meta.url));

test('locked metadata pruning preserves pins and rejects dependency or digest violations', async () => {
  const root = await mkdtemp(join(tmpdir(), 'synon-lock-prune-'));
  try {
    const prefix = join(root, 'prefix');
    await mkdir(join(prefix, 'conda-meta'), { recursive: true });
    for (const name of ['core', 'viewer']) {
      await writeFile(join(prefix, 'conda-meta', name + '.json'), JSON.stringify({
        name, version: '1.0', build: '0', build_number: 0, subdir: 'noarch',
        channel: 'conda-forge', url: `https://conda.anaconda.org/conda-forge/noarch/${name}-1.0-0.conda`,
        sha256: 'a'.repeat(64), license: 'MIT', depends: name === 'viewer' ? ['core >=1'] : [],
      }));
    }
    const args = ['--output-dir', join(root, 'locks'), '--catalog', join(root, 'manifest.json'),
      '--name', 'test', '--platform', 'linux-x86_64'];
    const run = (...extra) => spawnSync(process.execPath, [generator, ...args, ...extra], { encoding: 'utf8' });
    const original = run('--prefix', prefix);
    assert.equal(original.status, 0, original.stderr);
    const catalog = JSON.parse(await readFile(join(root, 'manifest.json')));
    const path = join(root, catalog.runtimes[0].manifestPath);
    const options = { name: 'test', platform: 'linux-x86_64' };
    const records = await readLockedRecords(path, { ...options, exclude: 'viewer' });
    assert.deepEqual(records.map((item) => item.decoded.name), ['core']);
    assert.equal(records[0].decoded.sha256, 'a'.repeat(64));
    await assert.rejects(readLockedRecords(path, { ...options, exclude: 'core' }), /required by viewer/);
    await assert.rejects(readLockedRecords(path, { ...options, exclude: 'missing' }), /not in the verified lock/);
    await assert.rejects(readLockedRecords(path, { ...options, platform: 'windows-x86_64' }), /identity/);
    const pruned = run('--locked-manifest', path, '--exclude-packages', 'viewer', '--required-packages', 'core=1.0');
    assert.equal(pruned.status, 0, pruned.stderr);
    const checked = run('--locked-manifest', path, '--exclude-packages', 'viewer', '--required-packages', 'core=1.0', '--check');
    assert.equal(checked.status, 0, checked.stderr);
    const invalid = run('--locked-manifest', path, '--exclude-packages', 'viewer', '--required-packages', 'viewer=1.0');
    assert.notEqual(invalid.status, 0);
    assert.match(invalid.stderr, /not present/);
    await writeFile(path, (await readFile(path, 'utf8')).replace('"version": "1.0"', '"version": "2.0"'));
    await assert.rejects(readLockedRecords(path, options), /digest/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
